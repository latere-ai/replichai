// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package project

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RepoForge changes a forge repository's visibility. The server's forge
// publisher is one; the call is idempotent on the forge, so applying a
// visibility a repository already has is not an error.
type RepoForge interface {
	SetPublic(ctx context.Context, repo string) error
	SetPrivate(ctx context.Context, repo string) error
}

// Retry timing for a repository whose flip failed: the wait doubles from
// repoRetryBase up to repoRetryMax, and the loop looks for due work every
// repoSweepEvery.
const (
	repoRetryBase  = 30 * time.Second
	repoRetryMax   = 15 * time.Minute
	repoSweepEvery = 30 * time.Second
	// repoBatch bounds how many due repositories one pass applies.
	repoBatch = 50
	// repoErrorMax bounds the stored text of the last failure.
	repoErrorMax = 500
)

// RepoVisibility records the visibility each repository should have and keeps
// retrying a flip until the forge has applied it. A project's publication is in
// this database and the repository's visibility is on the forge, so the two can
// disagree whenever the forge call fails after the project row has moved. The
// wanted visibility is written first, in Postgres, and the forge call is an
// attempt to make the forge match it: a failed attempt leaves the row pending,
// and Run attempts it again with a growing wait.
//
// It satisfies the SetPublic and SetPrivate pair the HTTP layer calls, so the
// handlers record a want by making the same calls they always made.
type RepoVisibility struct {
	pool  *pgxpool.Pool
	forge RepoForge
	clock func() time.Time
	every time.Duration
}

// NewRepoVisibility builds a RepoVisibility over a pool and the forge it applies
// to.
func NewRepoVisibility(pool *pgxpool.Pool, forge RepoForge) *RepoVisibility {
	return &RepoVisibility{pool: pool, forge: forge, clock: time.Now, every: repoSweepEvery}
}

// Owner is the organization the forge publishes under, when the forge says; the
// HTTP layer reads it to check the repository an imported session names.
func (v *RepoVisibility) Owner() string {
	if o, ok := v.forge.(interface{ Owner() string }); ok {
		return o.Owner()
	}
	return ""
}

// SetPublic records that a repository should be public and tries to make it so.
// It returns an error only when the want could not be recorded; a failed forge
// call is logged and retried by Run.
func (v *RepoVisibility) SetPublic(ctx context.Context, repo string) error {
	return v.want(ctx, repo, true)
}

// SetPrivate records that a repository should be private and tries to make it so.
func (v *RepoVisibility) SetPrivate(ctx context.Context, repo string) error {
	return v.want(ctx, repo, false)
}

func (v *RepoVisibility) want(ctx context.Context, repo string, public bool) error {
	if repo == "" {
		return errors.New("a repository visibility needs a repository")
	}
	// The upsert waits for a flip in flight on the same row, so the want it
	// writes is never overwritten by an attempt that read the earlier one.
	if _, err := v.pool.Exec(ctx, `
		INSERT INTO repo_visibility (repo, public, wanted_at, next_try_at)
		VALUES ($1, $2, $3, $3)
		ON CONFLICT (repo) DO UPDATE
			SET public = EXCLUDED.public, wanted_at = EXCLUDED.wanted_at, applied_at = NULL,
				attempts = 0, last_error = '', next_try_at = EXCLUDED.next_try_at`,
		repo, public, v.clock()); err != nil {
		return fmt.Errorf("record the visibility of %s: %w", repo, err)
	}
	if err := v.apply(ctx, repo); err != nil {
		slog.WarnContext(ctx, "repository visibility: not applied yet, will retry", "repo", repo, "public", public, "error", err)
	}
	return nil
}

// apply makes the forge match the recorded want of one repository. The row is
// locked for the forge call, so two appliers never race on one repository, and a
// row someone else is applying is skipped. A forge failure is recorded on the
// row and returned.
func (v *RepoVisibility) apply(ctx context.Context, repo string) error {
	return pgx.BeginFunc(ctx, v.pool, func(tx pgx.Tx) error {
		var public bool
		var attempts int
		err := tx.QueryRow(ctx, `
			SELECT public, attempts FROM repo_visibility
			WHERE repo = $1 AND applied_at IS NULL
			FOR UPDATE SKIP LOCKED`, repo).Scan(&public, &attempts)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // applied already, or another attempt holds it
		}
		if err != nil {
			return err
		}
		var forgeErr error
		if public {
			forgeErr = v.forge.SetPublic(ctx, repo)
		} else {
			forgeErr = v.forge.SetPrivate(ctx, repo)
		}
		if forgeErr == nil {
			_, err := tx.Exec(ctx, `UPDATE repo_visibility SET applied_at = $2, last_error = '' WHERE repo = $1`, repo, v.clock())
			return err
		}
		msg := forgeErr.Error()
		if len(msg) > repoErrorMax {
			msg = msg[:repoErrorMax]
		}
		if _, err := tx.Exec(ctx, `
			UPDATE repo_visibility SET attempts = attempts + 1, last_error = $2, next_try_at = $3
			WHERE repo = $1`, repo, msg, v.clock().Add(retryWait(attempts+1))); err != nil {
			return err
		}
		return forgeErr
	})
}

// retryWait is how long to wait after the nth failed attempt.
func retryWait(attempts int) time.Duration {
	wait := repoRetryBase
	for i := 1; i < attempts && wait < repoRetryMax; i++ {
		wait *= 2
	}
	return min(wait, repoRetryMax)
}

// Pending returns the repositories whose recorded visibility the forge has not
// applied yet.
func (v *RepoVisibility) Pending(ctx context.Context) ([]string, error) {
	rows, err := v.pool.Query(ctx, `SELECT repo FROM repo_visibility WHERE applied_at IS NULL ORDER BY wanted_at, repo`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// Sweep applies every repository that is due, once, and returns how many
// applied. A failure on one repository does not stop the others.
func (v *RepoVisibility) Sweep(ctx context.Context) (applied int, err error) {
	rows, err := v.pool.Query(ctx, `
		SELECT repo FROM repo_visibility
		WHERE applied_at IS NULL AND next_try_at <= $1
		ORDER BY next_try_at, repo LIMIT $2`, v.clock(), repoBatch)
	if err != nil {
		return 0, err
	}
	due, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	for _, repo := range due {
		if ctx.Err() != nil {
			return applied, ctx.Err()
		}
		if err := v.apply(ctx, repo); err != nil {
			slog.WarnContext(ctx, "repository visibility: retry failed", "repo", repo, "error", err)
			continue
		}
		applied++
	}
	return applied, nil
}

// Run applies pending visibilities until the context ends: once on entry, so
// work left by a restart resumes, then on every sweep interval. The server
// starts it in its own goroutine.
func (v *RepoVisibility) Run(ctx context.Context) {
	tick := time.NewTicker(v.every)
	defer tick.Stop()
	for {
		if _, err := v.Sweep(ctx); err != nil && ctx.Err() == nil {
			slog.WarnContext(ctx, "repository visibility: sweep failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
