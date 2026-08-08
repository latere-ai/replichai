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

// uniqueViolation is the SQLSTATE Postgres returns when an insert breaks a
// unique index — here, the double-trigger guard's partial indexes.
const uniqueViolation = "23505"

// PGStore is the Postgres implementation of Store. It satisfies the same
// contract the in-memory store does — the phase machine is enforced in Go's
// CanTransition, and Claim uses SELECT … FOR UPDATE SKIP LOCKED so two workers
// never take the same job.
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

// WithPGRates prices a settled session from the card in force when it started.
// Without it a metered store holds and releases the reserve but charges
// nothing.
func WithPGRates(c credit.CardSnapshot) PGOption {
	return func(s *PGStore) { s.card = c }
}

// snapshot reads the price list this session will be charged at. See the
// in-memory store's for why a fault and an unpriced deployment resolve
// differently.
func (s *PGStore) snapshot(ctx context.Context) (credit.Card, error) {
	return resolveCard(ctx, s.card)
}

// settleTx releases a session's hold and charges what it actually cost, inside
// the transaction that wrote its terminal phase. A session is therefore never
// terminal-but-unsettled, and settling twice is refused by the ledger's own
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
const jobColumns = `id, kind, principal, project_id, ref, paper_key, phase, status, repo_name, error, created_at, updated_at, phase_at, rate_card`

// scanJob reads one row in jobColumns order.
func scanJob(row pgx.Row) (Job, error) {
	var (
		j          Job
		statusRaw  []byte
		phaseAtRaw []byte
		ratesRaw   []byte
	)
	err := row.Scan(&j.ID, &j.Kind, &j.Principal, &j.ProjectID, &j.Ref, &j.PaperKey, &j.Phase,
		&statusRaw, &j.RepoName, &j.Err, &j.CreatedAt, &j.UpdatedAt, &phaseAtRaw, &ratesRaw)
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
// guard index (a non-terminal session for the same paper in this project, or
// anywhere this principal owns) surfaces as ErrActiveSession — a 409, not a 500.
//
// The credit gate runs inside the same transaction as the insert, serialised
// per project by an advisory lock. Reading the balance outside the transaction
// would be racy: under READ COMMITTED two concurrent submits at the boundary
// each read the other's uncommitted hold as absent, and both would be admitted
// (011, the structure 006l established for the counter).
func (s *PGStore) Enqueue(ctx context.Context, sub Submission) (Job, error) {
	if strings.TrimSpace(sub.Ref) == "" {
		return Job{}, ErrEmptyRef
	}
	id, err := newID()
	if err != nil {
		return Job{}, err
	}
	now := s.clock()
	status, _ := json.Marshal(provider.RunStatus{})
	phaseAt, _ := json.Marshal(map[Phase]time.Time{PhaseSubmitted: now})
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
			INSERT INTO jobs (id, kind, principal, project_id, ref, paper_key, phase, status, repo_name, error, created_at, updated_at, phase_at, rate_card)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,'','',$9,$9,$10,$11)
			RETURNING `+jobColumns,
			id, sub.Kind, sub.Principal, sub.Project, sub.Ref, sub.PaperKey, PhaseSubmitted, status, now, phaseAt, rates)
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

// checkQuotaTx refuses a submission past the payer's allowance, holding an
// takeReserveTx refuses a submission the project cannot afford to start, and
// holds the reserve against its pot — inside the transaction that inserts the
// session, so the balance it read cannot change underneath it and a new submit
// path cannot skip it.
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
	rows, err := s.pool.Query(ctx, `SELECT `+jobColumns+` FROM jobs WHERE principal = $1 ORDER BY created_at DESC, id DESC`, principal)
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
	rows, err := s.pool.Query(ctx, `SELECT `+jobColumns+` FROM jobs WHERE project_id = $1 ORDER BY created_at DESC, id DESC`, projectID)
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
			MAX(updated_at) AS latest_at
		FROM jobs WHERE project_id = ANY($1) GROUP BY project_id`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]ProjectStatus{}
	for rows.Next() {
		var id string
		var st ProjectStatus
		if err := rows.Scan(&id, &st.Total, &st.Running, &st.Latest, &st.LatestAt); err != nil {
			return nil, err
		}
		out[id] = st
	}
	return out, rows.Err()
}

// Cancel stops a non-terminal session with a guarded UPDATE — the WHERE clause
// is the guard, so a stop wins from any active phase and never routes through
// the phase machine. An already-terminal session is returned unchanged.
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
		// same transaction: a stopped session pays for the tokens it burned
		// before it stopped, and its remaining reserve goes back.
		return s.settleTx(ctx, tx, j)
	})
	if errors.Is(err, ErrNotFound) {
		// No active row updated: either it does not exist, or it is already
		// terminal. Return the current row (idempotent), or ErrNotFound.
		return s.Get(ctx, id)
	}
	return j, err
}

// Accrued sums what every in-flight session in a project has cost so far.
//
// The pricing is per row, in Go, because each session carries its own rate
// snapshot: a SQL sum would have to reimplement the price function against
// whatever card each row happens to hold, and the two would drift the first
// time a token class was added.
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
