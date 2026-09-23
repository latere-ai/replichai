// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package project

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrations is the project schema, in its own version namespace so it
// composes with job.Migrate and access.Migrate on one database. It runs after
// job.Migrate, because its backfill reads the jobs table.
var migrations = []struct {
	version int
	sql     string
}{
	{1, `
CREATE TABLE projects (
	id          TEXT PRIMARY KEY,
	owner       TEXT NOT NULL,
	title       TEXT NOT NULL DEFAULT '',
	visibility  TEXT NOT NULL DEFAULT 'private',
	created_at  TIMESTAMPTZ NOT NULL
);
CREATE INDEX projects_by_owner ON projects (owner, created_at DESC);

-- Backfill the entity layer from the sessions already in jobs (006g): every
-- distinct (principal, project_id) becomes a project, reusing the existing
-- project_id slug as the project id so each session's project_id already points
-- at a real project and nothing needs updating. The title is the slug for now,
-- because this is SQL only with no network; the real paper titles are a one-off
-- data fix after deploy.
INSERT INTO projects (id, owner, title, visibility, created_at)
SELECT project_id, principal, project_id, 'private', MIN(created_at)
FROM jobs
WHERE project_id <> ''
GROUP BY project_id, principal
ON CONFLICT (id) DO NOTHING;

-- The session to project foreign key, added NOT VALID: every new session must
-- reference a real project (Enqueue always assigns one), while any pre-existing
-- unkeyed row is exempted rather than blocking the migration.
ALTER TABLE jobs ADD CONSTRAINT jobs_project_fk
	FOREIGN KEY (project_id) REFERENCES projects (id) NOT VALID;
`},
	{2, `
-- The public-flip provenance (006f): the recorded responsibility sign-off.
ALTER TABLE projects ADD COLUMN signed_off_by       TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN signed_off_at       TIMESTAMPTZ;
ALTER TABLE projects ADD COLUMN attestation_version TEXT NOT NULL DEFAULT '';
`},
	{3, `
-- The owner's standing consent to cover their members' sessions (006l). Off by
-- default: sponsorship is always something the owner turned on.
ALTER TABLE projects ADD COLUMN sponsored BOOLEAN NOT NULL DEFAULT false;
`},
	{4, `
-- The signer snapshot (006n): who the owner said they were when they signed,
-- copied onto the project rather than read from their profile, so a later
-- profile edit never rewrites a published attestation. Every column defaults to
-- empty, which is exactly what a v1 sign-off, signed before this spec, holds;
-- the read path renders that as the date-only line it shows today.
ALTER TABLE projects ADD COLUMN signed_off_name        TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN signed_off_institution TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN signed_off_contact     TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN signed_off_orcid       TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN signed_off_scholar     TEXT NOT NULL DEFAULT '';
-- Already redacted when written; the full address is never stored.
ALTER TABLE projects ADD COLUMN signed_off_email       TEXT NOT NULL DEFAULT '';
`},
	{5, `
-- The retraction (006o). A withdrawal ends a publication without erasing the
-- sign-off it retracts, so these columns are beside the signed_off_* ones rather
-- than replacing them. NULL withdrawn_at means never withdrawn, which is the
-- state every project predating this spec is in, and the state SetPublic
-- restores.
ALTER TABLE projects ADD COLUMN withdrawn_at     TIMESTAMPTZ;
ALTER TABLE projects ADD COLUMN withdrawn_by     TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN withdrawn_reason TEXT NOT NULL DEFAULT '';
`},
	{6, `
-- 011 makes a project pay for its own sessions out of its own credits, so the
-- owner's standing consent to cover a collaborator's run no longer has a
-- meaning. A project holding money is that consent, expressed once and in the
-- only unit that can be drawn down.
ALTER TABLE projects DROP COLUMN sponsored;
`},
	{7, `
-- This project's own overdraft floor, overriding the platform default (011).
-- Zero means the platform default applies. It is not exposed on any payload a
-- member reads, because a published overdraft becomes an allowance somebody
-- plans around.
ALTER TABLE projects ADD COLUMN credit_cap BIGINT NOT NULL DEFAULT 0;
`},
	{8, `
-- The papers a project is about (012). Before this a paper existed only as the
-- ref of a session that had been started, so a project could not hold a paper it
-- had not run, and a paper whose only session failed left no record of having
-- been part of the work.
--
-- The key is paper.Key, the same offline identity a session stores in
-- jobs.paper_key, which joins a session to its paper without a column on either
-- side.
CREATE TABLE project_papers (
	project_id TEXT NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
	paper_key  TEXT NOT NULL,
	ref        TEXT NOT NULL,
	title      TEXT NOT NULL DEFAULT '',
	added_by   TEXT NOT NULL,
	added_at   TIMESTAMPTZ NOT NULL,
	PRIMARY KEY (project_id, paper_key)
);

-- Backfill from the sessions already run: every distinct (project, paper) pair
-- in jobs is a paper its project is about, whatever became of the run. The
-- earliest session dates the paper and its principal is credited with adding
-- it, which is accurate, because that submit is how the paper entered the
-- project. The ref is that session's ref; the title is left blank rather than
-- guessed, and is filled in the next time the paper resolves.
INSERT INTO project_papers (project_id, paper_key, ref, title, added_by, added_at)
SELECT DISTINCT ON (j.project_id, j.paper_key)
	j.project_id, j.paper_key, j.ref, '', j.principal, j.created_at
FROM jobs j
JOIN projects p ON p.id = j.project_id
WHERE j.paper_key <> '' AND j.project_id <> ''
ORDER BY j.project_id, j.paper_key, j.created_at
ON CONFLICT (project_id, paper_key) DO NOTHING;
`},
	{9, `
-- The trash (019). A project an owner deleted leaves every list without taking
-- its papers, its sessions, or its published repositories with it, so the act is
-- reversible until the owner confirms it.
--
-- A column rather than a second table: project_papers and jobs.project_id both
-- reference this row, and moving a row means moving everything that points at
-- it. NULL means not in the trash, which is the state of every project
-- predating this migration.
ALTER TABLE projects ADD COLUMN deleted_at TIMESTAMPTZ;

-- The two reads that filter on it: an owner's list, and the trash screen.
CREATE INDEX projects_by_owner_live ON projects (owner, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX projects_trashed ON projects (owner, deleted_at DESC) WHERE deleted_at IS NOT NULL;
`},
	{10, `
-- The paper's bibliographic record, not only its title (037). The record comes
-- from the identifier's own metadata service or the publisher's own
-- declaration, and resolution was already fetching all of it and keeping only
-- the title.
--
-- Columns rather than a jsonb blob: these are the fields a citation is made of,
-- they are the same for every paper, and a screen renders them one by one.
ALTER TABLE project_papers ADD COLUMN authors   TEXT[] NOT NULL DEFAULT '{}';
ALTER TABLE project_papers ADD COLUMN venue     TEXT   NOT NULL DEFAULT '';
ALTER TABLE project_papers ADD COLUMN year      INT    NOT NULL DEFAULT 0;
ALTER TABLE project_papers ADD COLUMN volume    TEXT   NOT NULL DEFAULT '';
ALTER TABLE project_papers ADD COLUMN issue     TEXT   NOT NULL DEFAULT '';
ALTER TABLE project_papers ADD COLUMN pages     TEXT   NOT NULL DEFAULT '';
ALTER TABLE project_papers ADD COLUMN publisher TEXT   NOT NULL DEFAULT '';

-- Which fields a person corrected. Enrichment runs without the person's
-- involvement, from a button, a sweep, or a re-add, and a refresh that quietly
-- replaced a name somebody had fixed would make fixing it pointless. Named
-- fields rather than a single edited flag, because correcting one author's
-- name must not freeze the year nobody has looked up yet.
ALTER TABLE project_papers ADD COLUMN edited TEXT[] NOT NULL DEFAULT '{}';
`},
	{11, `
-- What the project is about, in its owner's own words (038). A title is either
-- what somebody typed or the first paper's reference, so half the projects here
-- are named after a doi: string and none of them can explain why they exist.
--
-- Empty rather than NULL: every project that predates this has no description,
-- which is a state the screen already has to render, not a missing value.
ALTER TABLE projects ADD COLUMN description TEXT NOT NULL DEFAULT '';
`},
	{12, `
-- The paper's own abstract, as its source publishes it (037). A title says what
-- a paper is called and the record says where it appeared; this is the only
-- field that says what it is about, which is what somebody reading a project
-- they did not assemble needs.
--
-- Empty for the many records that carry none: Crossref has an abstract only
-- where the publisher deposited one, and a paper without one shows none rather
-- than a summary somebody wrote for it.
ALTER TABLE project_papers ADD COLUMN abstract TEXT NOT NULL DEFAULT '';
`},
}

// migrationLock is this migrator's advisory lock id. A rolling deploy runs the
// old and new pods at once (maxSurge), so two processes migrate the same
// database concurrently: both find a version missing, both run its DDL, and
// the loser fails on "column already exists". Without the lock the failure
// resolves itself on restart, but it crash-loops a pod on every schema change
// until then.
//
// The lock is taken inside the migration transaction and released with it, so
// the second process waits, then reads the version the first recorded and
// skips it. Each namespace has its own id, so the five migrators only ever
// serialize against another copy of themselves.
const migrationLock = 5

// Migrate applies the project schema and the one-time backfill. It is safe to
// run on every startup, and it must run after job.Migrate.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// The table is created under the same lock the versions are applied
	// under. IF NOT EXISTS is not atomic against a concurrent creator: two
	// sessions both find the table absent, both run the DDL, and the loser
	// fails on a duplicate key in pg_type. That is the crash this lock exists
	// to prevent, and without the lock here it would happen one statement
	// before the lock was taken.
	if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS project_migrations (
				version    INT PRIMARY KEY,
				applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
			)`)
		return err
	}); err != nil {
		return fmt.Errorf("create project migrations table: %w", err)
	}
	for _, m := range migrations {
		if err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, migrationLock); err != nil {
				return err
			}
			var exists bool
			if err := tx.QueryRow(ctx,
				`SELECT EXISTS(SELECT 1 FROM project_migrations WHERE version=$1)`, m.version).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return nil
			}
			if _, err := tx.Exec(ctx, m.sql); err != nil {
				return fmt.Errorf("apply project migration %d: %w", m.version, err)
			}
			_, err := tx.Exec(ctx, `INSERT INTO project_migrations (version) VALUES ($1)`, m.version)
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}
