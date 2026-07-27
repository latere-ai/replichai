package job

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/provider"
)

// PGStore is the Postgres implementation of Store. It satisfies the same
// contract the in-memory store does — the phase machine is enforced in Go's
// CanTransition, and Claim uses SELECT … FOR UPDATE SKIP LOCKED so two workers
// never take the same job.
type PGStore struct {
	pool  *pgxpool.Pool
	clock func() time.Time
}

// NewPGStore builds a store over a pgx pool.
func NewPGStore(pool *pgxpool.Pool) *PGStore {
	return &PGStore{pool: pool, clock: time.Now}
}

// jobColumns is the column list every read selects, in scan order.
const jobColumns = `id, kind, principal, project_id, ref, phase, status, repo_name, error, created_at, updated_at, phase_at`

// scanJob reads one row in jobColumns order.
func scanJob(row pgx.Row) (Job, error) {
	var (
		j          Job
		statusRaw  []byte
		phaseAtRaw []byte
	)
	err := row.Scan(&j.ID, &j.Kind, &j.Principal, &j.ProjectID, &j.Ref, &j.Phase,
		&statusRaw, &j.RepoName, &j.Err, &j.CreatedAt, &j.UpdatedAt, &phaseAtRaw)
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
	return j, nil
}

// Enqueue inserts a new submitted job.
func (s *PGStore) Enqueue(ctx context.Context, principal, ref string, k Kind, project string) (Job, error) {
	if strings.TrimSpace(ref) == "" {
		return Job{}, ErrEmptyRef
	}
	id, err := newID()
	if err != nil {
		return Job{}, err
	}
	now := s.clock()
	status, _ := json.Marshal(provider.RunStatus{})
	phaseAt, _ := json.Marshal(map[Phase]time.Time{PhaseSubmitted: now})

	row := s.pool.QueryRow(ctx, `
		INSERT INTO jobs (id, kind, principal, project_id, ref, phase, status, repo_name, error, created_at, updated_at, phase_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'','',$8,$8,$9)
		RETURNING `+jobColumns,
		id, k, principal, project, ref, PhaseSubmitted, status, now, phaseAt)
	return scanJob(row)
}

// Get returns one job by id.
func (s *PGStore) Get(ctx context.Context, id string) (Job, error) {
	return scanJob(s.pool.QueryRow(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id = $1`, id))
}

// Claim atomically takes the oldest submitted job into resolving. FOR UPDATE
// SKIP LOCKED is what makes concurrent claims disjoint under real contention.
func (s *PGStore) Claim(ctx context.Context) (Job, bool, error) {
	now := s.clock()
	row := s.pool.QueryRow(ctx, `
		UPDATE jobs SET
			phase = $1,
			updated_at = $2,
			phase_at = phase_at || jsonb_build_object($3::text, to_jsonb($2::timestamptz))
		WHERE id = (
			SELECT id FROM jobs WHERE phase = $4
			ORDER BY created_at
			FOR UPDATE SKIP LOCKED
			LIMIT 1
		)
		RETURNING `+jobColumns,
		PhaseResolving, now, string(PhaseResolving), PhaseSubmitted)
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

		status, _ := json.Marshal(j.Status)
		phaseAt, _ := json.Marshal(j.PhaseAt)
		_, err = tx.Exec(ctx, `
			UPDATE jobs SET phase=$2, ref=$3, project_id=$4, repo_name=$5, error=$6,
				status=$7, updated_at=$8, phase_at=$9
			WHERE id=$1`,
			id, j.Phase, j.Ref, j.ProjectID, j.RepoName, j.Err, status, now, phaseAt)
		return err
	})
}

// Observe records the latest status without a phase change.
func (s *PGStore) Observe(ctx context.Context, id string, status provider.RunStatus) error {
	raw, _ := json.Marshal(status)
	tag, err := s.pool.Exec(ctx, `UPDATE jobs SET status=$2, updated_at=$3 WHERE id=$1`, id, raw, s.clock())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListByPrincipal returns a principal's jobs, newest first.
func (s *PGStore) ListByPrincipal(ctx context.Context, principal string) ([]Job, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+jobColumns+` FROM jobs WHERE principal = $1 ORDER BY created_at DESC`, principal)
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
