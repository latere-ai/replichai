// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/provider"
)

// uniqueViolation is the SQLSTATE Postgres returns when an insert violates a
// unique index, which here are the double-trigger guard's partial indexes.
const uniqueViolation = "23505"

// PGStore is the Postgres implementation of Store. It satisfies the same
// contract as the in-memory store: the phase machine is enforced by
// CanTransition in Go, and Claim uses SELECT ... FOR UPDATE SKIP LOCKED so two
// workers never take the same job.
type PGStore struct {
	pool  *pgxpool.Pool
	clock func() time.Time
	// credits and reserve are the enqueue gate (011). Both nil means unmetered.
	credits *credit.PGStore
	reserve credit.Reserve
	card    credit.CardSnapshot
}

// PGOption configures a PGStore.
type PGOption func(*PGStore)

// WithPGCredits meters submissions against the project's credits (011).
// Without it the store is unmetered.
func WithPGCredits(c *credit.PGStore, r credit.Reserve) PGOption {
	return func(s *PGStore) { s.credits, s.reserve = c, r }
}

// WithPGClock sets the time source. A lease records when a driver was last
// alive, so a test of its expiry advances the clock rather than waiting.
func WithPGClock(f func() time.Time) PGOption {
	return func(s *PGStore) { s.clock = f }
}

// WithPGRates prices a settled session from the card in force when it started.
// Without it a metered store holds and releases the reserve but charges
// nothing.
func WithPGRates(c credit.CardSnapshot) PGOption {
	return func(s *PGStore) { s.card = c }
}

// snapshot reads the price list this session will be charged at. See
// MemStore.snapshot for why a fault and an unpriced deployment resolve
// differently.
func (s *PGStore) snapshot(ctx context.Context) (credit.Card, error) {
	return resolveCard(ctx, s.card)
}

// settleTx releases a session's hold and charges what it cost, inside the
// transaction that wrote its terminal phase. A session is therefore never
// terminal and unsettled, and a second settlement is refused by the ledger's
// partial unique index rather than by a check here.
func (s *PGStore) settleTx(ctx context.Context, tx pgx.Tx, j Job) error {
	if s.credits == nil || j.ProjectID == "" {
		return nil
	}
	_, err := s.credits.SettleTx(ctx, tx, credit.Pot(j.ProjectID), j.ID, j.cost())
	return err
}

// NewPGStore builds a store over a pgx pool.
func NewPGStore(pool *pgxpool.Pool, opts ...PGOption) *PGStore {
	s := &PGStore{pool: pool, clock: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// jobColumns is the column list every read selects, in scan order.
const jobColumns = `id, kind, principal, project_id, ref, paper_key, phase, status, repo_name, error, created_at, updated_at, phase_at, rate_card, tier, origin, external_id`

// encodeStatus serialises a run status for the status column.
//
// The error is returned rather than dropped because json.Marshal returns nil
// bytes when it fails. Writing those would put a NULL over the row's usage,
// and because a session's cost is derived from the usage, the charge would
// silently disappear. The field that can fail to encode is Status.SessionRef,
// a json.RawMessage holding whatever the caller put there.
func encodeStatus(status provider.RunStatus) ([]byte, error) {
	raw, err := json.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("encode status: %w", err)
	}
	return raw, nil
}

// encodePhaseAt serialises the phase timeline for the phase_at column. The
// reasoning is the same as for encodeStatus: time.Time fails to encode a year
// outside [0,9999], and the clock behind these timestamps is injectable, so a
// bad one must fail the write rather than erase when each phase was entered.
func encodePhaseAt(phaseAt map[Phase]time.Time) ([]byte, error) {
	raw, err := json.Marshal(phaseAt)
	if err != nil {
		return nil, fmt.Errorf("encode phase_at: %w", err)
	}
	return raw, nil
}

// scanJob reads one row in jobColumns order.
func scanJob(row pgx.Row) (Job, error) {
	var (
		j          Job
		statusRaw  []byte
		phaseAtRaw []byte
		ratesRaw   []byte
	)
	err := row.Scan(&j.ID, &j.Kind, &j.Principal, &j.ProjectID, &j.Ref, &j.PaperKey, &j.Phase,
		&statusRaw, &j.RepoName, &j.Err, &j.CreatedAt, &j.UpdatedAt, &phaseAtRaw, &ratesRaw, &j.Tier,
		&j.Origin, &j.ExternalID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	if len(statusRaw) > 0 {
		if err := json.Unmarshal(statusRaw, &j.Status); err != nil {
			return Job{}, fmt.Errorf("decode status: %w", err)
		}
	}
	if len(phaseAtRaw) > 0 {
		if err := json.Unmarshal(phaseAtRaw, &j.PhaseAt); err != nil {
			return Job{}, fmt.Errorf("decode phase_at: %w", err)
		}
	}
	if len(ratesRaw) > 0 {
		if err := json.Unmarshal(ratesRaw, &j.Rates); err != nil {
			return Job{}, fmt.Errorf("decode rate_card: %w", err)
		}
	}
	// Every read goes through here, so the derived cost is filled in once.
	return j.withCost(), nil
}

// Enqueue inserts a new submitted job. A unique_violation on either partial
// guard index, meaning a non-terminal session for the same paper exists in
// this project or in any project this principal owns, is returned as
// ErrActiveSession, which maps to a 409 rather than a 500.
//
// The credit gate runs inside the same transaction as the insert, serialised
// per project by an advisory lock. Reading the balance outside the transaction
// would be racy: under READ COMMITTED two concurrent submits at the boundary
// would each read the other's uncommitted hold as absent, and both would be
// admitted (011, following the structure 006l established for the counter).
func (s *PGStore) Enqueue(ctx context.Context, sub Submission) (Job, error) {
	if strings.TrimSpace(sub.Ref) == "" {
		return Job{}, ErrEmptyRef
	}
	id := newID()
	now := s.clock()
	status, err := encodeStatus(provider.RunStatus{})
	if err != nil {
		return Job{}, err
	}
	phaseAt, err := encodePhaseAt(map[Phase]time.Time{PhaseSubmitted: now})
	if err != nil {
		return Job{}, err
	}
	card, err := s.snapshot(ctx)
	if err != nil {
		return Job{}, err
	}
	rates, err := json.Marshal(card)
	if err != nil {
		return Job{}, err
	}

	var j Job
	err = pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if err := s.takeReserveTx(ctx, tx, sub.Project, id); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `
			INSERT INTO jobs (id, kind, principal, project_id, ref, paper_key, phase, status, repo_name, error, created_at, updated_at, phase_at, rate_card, tier)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'','',$9,$9,$10,$11,$12)
			RETURNING `+jobColumns,
			id, sub.Kind, sub.Principal, sub.Project, sub.Ref, sub.PaperKey, PhaseSubmitted, status, now, phaseAt, rates, sub.Tier)
		var err error
		j, err = scanJob(row)
		return err
	})
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation &&
		(pgErr.ConstraintName == "jobs_active_paper_project" || pgErr.ConstraintName == "jobs_active_paper_principal") {
		return Job{}, ErrActiveSession
	}
	if err != nil {
		return Job{}, err
	}
	return j, nil
}

// ImportLocal records a session executed on somebody's own machine. It is a
// separate statement from Enqueue rather than a flag on it: this function
// never calls takeReserveTx, and no branch in it could. Whoever ran the
// session bought the compute from the vendor, so nothing about it reaches the
// ledger.
func (s *PGStore) ImportLocal(ctx context.Context, in Import) (Job, error) {
	if strings.TrimSpace(in.Ref) == "" {
		return Job{}, ErrEmptyRef
	}
	if !in.Phase.Terminal() {
		return Job{}, fmt.Errorf("%w: a session in phase %q is not a record", ErrIllegalTransition, in.Phase)
	}
	if strings.TrimSpace(in.Runtime) == "" || strings.TrimSpace(in.ExternalID) == "" {
		return Job{}, fmt.Errorf("%w: an imported session needs a runtime and an id", ErrEmptyRef)
	}
	id := newID()
	now := s.clock()
	started, finished := in.StartedAt, in.FinishedAt
	if started.IsZero() {
		started = now
	}
	if finished.IsZero() {
		finished = now
	}
	status, err := encodeStatus(in.Status)
	if err != nil {
		return Job{}, err
	}
	phaseAt, err := encodePhaseAt(map[Phase]time.Time{PhaseSubmitted: started, in.Phase: finished})
	if err != nil {
		return Job{}, err
	}
	// The insert is idempotent on (origin, principal, external id), which the
	// partial unique index enforces: an operator's re-push updates their own
	// row rather than opening a second session for one run, and cannot find
	// another operator's row. The update is also restricted to the project the
	// record already belongs to: a row that would move projects is left
	// unchanged and the conflict is reported, so a re-push cannot move a
	// record.
	row := s.pool.QueryRow(ctx, `
		INSERT INTO jobs (id, kind, principal, project_id, ref, paper_key, phase, status, repo_name, error,
		                  created_at, updated_at, phase_at, rate_card, tier, origin, external_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,'{}','',$14,$15)
		ON CONFLICT (origin, principal, external_id) WHERE external_id <> '' DO UPDATE SET
			ref = EXCLUDED.ref, paper_key = EXCLUDED.paper_key,
			phase = EXCLUDED.phase, status = EXCLUDED.status, repo_name = EXCLUDED.repo_name,
			error = EXCLUDED.error, updated_at = EXCLUDED.updated_at, phase_at = EXCLUDED.phase_at
		WHERE jobs.project_id = EXCLUDED.project_id
		RETURNING `+jobColumns,
		id, KindAudit, in.Principal, in.Project, in.Ref, in.PaperKey, in.Phase, status, in.RepoName, in.Err,
		started, now, phaseAt, OriginLocal, in.ExternalID)
	imported, err := scanJob(row)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrNotFound) {
		// No row came back: the conflict clause found the record under another
		// project and left it unchanged. scanJob reports an empty result as a
		// missing job, which this is not.
		return Job{}, ErrImportedElsewhere
	}
	return imported, err
}

// takeReserveTx refuses a submission the project cannot afford to start, and
// holds the reserve against its pot. It runs inside the transaction that
// inserts the session, so the balance it read cannot change before the insert
// and a new submit path cannot skip it.
func (s *PGStore) takeReserveTx(ctx context.Context, tx pgx.Tx, project, jobID string) error {
	if s.credits == nil || s.reserve == nil {
		return nil
	}
	amount, err := s.reserve(ctx)
	if err != nil {
		return err
	}
	if amount <= 0 {
		return nil
	}
	if err := s.credits.HoldTx(ctx, tx, credit.Pot(project), jobID, amount); err != nil {
		if errors.Is(err, credit.ErrInsufficient) {
			return ErrNoCredits
		}
		return err
	}
	return nil
}

// Get returns one job by id. A deleted session returns ErrNotFound (032). This
// single check covers every per-session read, because the job, its transcript,
// verdict, artifacts, event stream and cancel all resolve through Get.
func (s *PGStore) Get(ctx context.Context, id string) (Job, error) {
	return scanJob(s.pool.QueryRow(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE id = $1 AND deleted_at IS NULL`, id))
}

// Claim atomically moves the oldest submitted job into resolving. FOR UPDATE
// SKIP LOCKED makes concurrent claims disjoint under contention.
func (s *PGStore) Claim(ctx context.Context, worker string) (Job, bool, error) {
	now := s.clock()
	row := s.pool.QueryRow(ctx, `
		UPDATE jobs SET
			phase = $1,
			updated_at = $2,
			phase_at = phase_at || jsonb_build_object($3::text, to_jsonb($2::timestamptz)),
			adopted_by = $5,
			adopted_at = $2
		WHERE id = (
			SELECT id FROM jobs WHERE phase = $4
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING `+jobColumns,
		PhaseResolving, now, string(PhaseResolving), PhaseSubmitted, worker)
	j, err := scanJob(row)
	if errors.Is(err, ErrNotFound) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	return j, true, nil
}

// Advance moves a job through the one transition function under a row lock,
// refusing anything CanTransition forbids.
func (s *PGStore) Advance(ctx context.Context, id string, to Phase, mutate func(*Job)) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		j, err := scanJob(tx.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = $1 FOR UPDATE`, id))
		if err != nil {
			return err
		}
		if !CanTransition(j.Phase, to) {
			return illegal(j.Phase, to)
		}
		if mutate != nil {
			mutate(&j)
		}
		now := s.clock()
		j.Phase = to
		j.UpdatedAt = now
		if j.PhaseAt == nil {
			j.PhaseAt = map[Phase]time.Time{}
		}
		j.PhaseAt[to] = now

		status, err := encodeStatus(j.Status)
		if err != nil {
			return err
		}
		phaseAt, err := encodePhaseAt(j.PhaseAt)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `
			UPDATE jobs SET phase=$2, ref=$3, project_id=$4, repo_name=$5, error=$6,
				status=$7, updated_at=$8, phase_at=$9
			WHERE id=$1`,
			id, j.Phase, j.Ref, j.ProjectID, j.RepoName, j.Err, status, now, phaseAt); err != nil {
			return err
		}
		if to.Terminal() {
			return s.settleTx(ctx, tx, j)
		}
		return nil
	})
}

// Observe records the latest status without a phase change.
func (s *PGStore) Observe(ctx context.Context, id string, status provider.RunStatus) error {
	raw, err := encodeStatus(status)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET
			status = $2,
			updated_at = $3,
			adopted_at = CASE WHEN adopted_at IS NULL THEN NULL ELSE $3::timestamptz END
		WHERE id = $1`, id, raw, s.clock())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Adopt leases a job in flight to one driver and reports whether this caller
// won it. The WHERE clause makes the whole decision, so two replicas
// reconciling the same row at the same instant cannot both win it.
func (s *PGStore) Adopt(ctx context.Context, id, worker string, ttl time.Duration) (bool, error) {
	now := s.clock()
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET adopted_by=$2, adopted_at=$3
		WHERE id=$1
		  AND phase NOT IN ('done','failed')
		  AND (adopted_at IS NULL OR adopted_by=$2 OR adopted_at < $4)`,
		id, worker, now, now.Add(-ttl))
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Touch refreshes this worker's lease and reports whether it still holds it.
// The WHERE clause makes the whole decision: a row that went terminal, or one
// another replica has adopted, matches nothing and reports held=false.
func (s *PGStore) Touch(ctx context.Context, id, worker string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `
		UPDATE jobs SET adopted_at=$3
		WHERE id=$1 AND adopted_by=$2 AND phase NOT IN ('done','failed')`,
		id, worker, s.clock())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ListByPrincipal returns a principal's jobs, newest first.
func (s *PGStore) ListByPrincipal(ctx context.Context, principal string) ([]Job, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+jobColumns+
		` FROM jobs WHERE principal = $1 AND deleted_at IS NULL ORDER BY created_at DESC, id DESC`, principal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// ListByProject returns a project's sessions, newest first.
func (s *PGStore) ListByProject(ctx context.Context, projectID string) ([]Job, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+jobColumns+
		` FROM jobs WHERE project_id = $1 AND deleted_at IS NULL ORDER BY created_at DESC, id DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// DeleteByProject removes every session belonging to a project, idempotently.
func (s *PGStore) DeleteByProject(ctx context.Context, projectID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM jobs WHERE project_id = $1`, projectID)
	return err
}

// MovePaperSessionsTx moves one paper's sessions to another project, inside
// the transaction that moves the paper row itself (035). It is the job store's
// half of project.MovePaper and runs nowhere else.
//
// Deleted sessions move too. They are hidden from every read except SpendSince,
// which still counts them, so leaving one behind would leave a charge under a
// project that no longer holds the paper it paid for.
//
// updated_at is deliberately not changed: SpendSince windows on it, so
// updating it here would pull months-old sessions into the 30-day figure and
// inflate the spend of both projects at once.
func (s *PGStore) MovePaperSessionsTx(ctx context.Context, tx pgx.Tx, fromID, toID, key string) error {
	_, err := tx.Exec(ctx,
		`UPDATE jobs SET project_id=$2 WHERE project_id=$1 AND paper_key=$3`, fromID, toID, key)
	return err
}

// Delete marks one session deleted (032). It is idempotent: the WHERE clause
// makes a second call a no-op rather than updating the timestamp, so the
// recorded deletion time is the first deletion, not the last.
func (s *PGStore) Delete(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE jobs SET deleted_at = $2 WHERE id = $1 AND deleted_at IS NULL`, id, s.clock())
	return err
}

// StatusByProjects summarises the named projects' sessions in one query. The
// running filter uses the same terminal set as Phase.Terminal, so the in-memory
// and Postgres stores agree.
func (s *PGStore) StatusByProjects(ctx context.Context, ids []string) (map[string]ProjectStatus, error) {
	if len(ids) == 0 {
		return map[string]ProjectStatus{}, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT project_id,
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE phase NOT IN ('done','failed')) AS running,
			(array_agg(phase ORDER BY created_at DESC, id DESC))[1] AS latest,
			COUNT(*) FILTER (WHERE repo_name <> '') AS published,
			MAX(updated_at) AS latest_at
		FROM jobs WHERE project_id = ANY($1) AND deleted_at IS NULL GROUP BY project_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ProjectStatus{}
	for rows.Next() {
		var id string
		var st ProjectStatus
		if err := rows.Scan(&id, &st.Total, &st.Running, &st.Latest, &st.Published, &st.LatestAt); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// SpendSince sums each project's session costs from since onwards.
//
// The rows are fetched and summed in Go rather than aggregated in SQL: cost is
// derived from the usage in status against the rate_card the session carries,
// and a second implementation of that pricing in SQL could disagree with the
// first. The window bounds the row count.
//
// Deleted sessions are counted, and this is the only read path where they are
// (032). Their charge is still in the ledger, and dropping it here would show
// an owner one total in the rail and another in their wallet.
func (s *PGStore) SpendSince(ctx context.Context, ids []string, since time.Time) (map[string]int64, error) {
	if len(ids) == 0 {
		return map[string]int64{}, nil
	}
	// A locally executed session is left out. The rail shows what this project
	// has cost, and this deployment charged nothing for such a session. This is
	// the opposite of 035's moved session, which is still counted because the
	// ledger still holds its charge.
	rows, err := s.pool.Query(ctx, `SELECT `+jobColumns+
		` FROM jobs WHERE project_id = ANY($1) AND updated_at >= $2 AND origin <> $3`,
		ids, since, OriginLocal)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out[j.ProjectID] += j.withCost().Cost
	}
	return out, rows.Err()
}

// Cancel stops a non-terminal session with a guarded UPDATE. The WHERE clause
// is the guard, so a stop succeeds from any active phase and does not go
// through the phase machine. An already-terminal session is returned unchanged.
func (s *PGStore) Cancel(ctx context.Context, id string) (Job, error) {
	now := s.clock()
	var j Job
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		row := tx.QueryRow(ctx, `
			UPDATE jobs SET phase='failed', error=$2, updated_at=$3,
				phase_at = phase_at || jsonb_build_object('failed', to_jsonb($3::timestamptz))
			WHERE id=$1 AND phase NOT IN ('done','failed')
			RETURNING `+jobColumns,
			id, CancelCause, now)
		var err error
		if j, err = scanJob(row); err != nil {
			return err
		}
		// Cancelling is a terminal write like any other, so it settles in the
		// same transaction: a stopped session pays for the tokens it used before
		// it stopped, and its remaining reserve is released.
		return s.settleTx(ctx, tx, j)
	})
	if errors.Is(err, ErrNotFound) {
		// No active row was updated: either it does not exist, or it is already
		// terminal. Return the current row, so the call is idempotent, or
		// ErrNotFound.
		return s.Get(ctx, id)
	}
	return j, err
}

// Accrued sums what every in-flight session in a project has cost so far.
//
// The pricing is per row, in Go, because each session carries its own rate
// snapshot. A SQL sum would have to reimplement the price function against
// whatever card each row holds, and the two would diverge the first time a
// token class was added.
func (s *PGStore) Accrued(ctx context.Context, project string) (int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+jobColumns+` FROM jobs WHERE project_id = $1 AND phase NOT IN ($2,$3)`,
		project, PhaseDone, PhaseFailed)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var n int64
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return 0, err
		}
		n += j.cost()
	}
	return n, rows.Err()
}

// Active returns every non-terminal job.
func (s *PGStore) Active(ctx context.Context) ([]Job, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+jobColumns+` FROM jobs WHERE phase NOT IN ($1,$2) ORDER BY created_at`,
		PhaseDone, PhaseFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
