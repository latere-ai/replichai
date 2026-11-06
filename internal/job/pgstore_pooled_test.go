// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package job

import (
	"context"
	"encoding/json/jsontext"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/provider"
)

// execModeDSN pins the strictest exec mode onto a DSN, deliberately. The
// family's pooled DSNs carry cache_describe, under which the driver asks the
// server to describe each statement, learns the parameter types and encodes
// accordingly, so a byte slice bound to a jsonb column survives there. Exec
// mode sends every parameter in the text format, which is the only setting
// that makes the Go type behind a jsonb parameter observable, so a test run
// under it proves the binding is right under any mode.
func execModeDSN(dsn string) string {
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "default_query_exec_mode=exec"
}

// TestPGStoreWritesJSONColumnsUnderExecMode pins how the session store's four
// jsonb columns reach the database.
//
// A Go byte slice is sent as bytea. When the driver knows the parameter's
// type it encodes correctly, but when parameters travel in the text format the
// value arrives as a hex literal and `\x7b22...`::jsonb fails with SQLSTATE
// 22P02. The serving pool is opened in exec mode to hold the store to the
// stricter of the two, so the binding cannot silently depend on a DSN setting;
// see execModeDSN.
//
// Migrations run on the direct endpoint, as they do in the deployment. Every
// statement the store writes a jsonb parameter from is exercised: submitting a
// session, importing one run elsewhere, advancing a phase, and recording an
// observation.
func TestPGStoreWritesJSONColumnsUnderExecMode(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run the pooled json-column test")
	}
	ctx := context.Background()

	direct, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect to the direct endpoint: %v", err)
	}
	defer direct.Close()
	if err := Migrate(ctx, direct); err != nil {
		t.Fatalf("migrate on the direct endpoint: %v", err)
	}
	if _, err := direct.Exec(ctx, `TRUNCATE jobs`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	serving, err := pgxpool.New(ctx, execModeDSN(url))
	if err != nil {
		t.Fatalf("connect to the serving endpoint: %v", err)
	}
	defer serving.Close()

	s := NewPGStore(serving)

	// Enqueue writes status, phase_at and rate_card in one statement.
	j, err := s.Enqueue(ctx, Submission{
		Principal: "owner", Ref: "arxiv:2401.00001", Kind: KindAudit,
		Project: "proj", PaperKey: "arxiv:2401.00001",
	})
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if j.PhaseAt[PhaseSubmitted].IsZero() {
		t.Error("phase_at came back without the submitted timestamp")
	}

	// Advance writes status and phase_at from the transition statement.
	if err := s.Advance(ctx, j.ID, PhaseResolving, nil); err != nil {
		t.Fatalf("Advance: %v", err)
	}

	// Observe writes status on its own.
	if err := s.Observe(ctx, j.ID, provider.RunStatus{
		State: provider.StateRunning,
		Usage: provider.Usage{InputTokens: 42},
	}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	got, err := s.Get(ctx, j.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Usage.InputTokens != 42 {
		t.Errorf("observed usage = %d, want the 42 that was written", got.Status.Usage.InputTokens)
	}
	if got.Phase != PhaseResolving {
		t.Errorf("phase = %s, want resolving", got.Phase)
	}

	// ImportLocal writes status and phase_at from its own statement, which
	// carries a SessionRef the caller supplied as raw json.
	imported, err := s.ImportLocal(ctx, Import{
		Principal: "owner", Project: "proj", Ref: "arxiv:2401.00002",
		PaperKey: "arxiv:2401.00002", Runtime: "local", ExternalID: "run-1",
		Phase: PhaseDone,
		Status: provider.RunStatus{
			State:      provider.StateDone,
			SessionRef: jsontext.Value(`{"runtime":"local"}`),
			Usage:      provider.Usage{OutputTokens: 7},
		},
		RepoName:   "owner/repro",
		StartedAt:  time.Now().Add(-time.Hour),
		FinishedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("ImportLocal: %v", err)
	}
	if imported.Status.Usage.OutputTokens != 7 {
		t.Errorf("imported usage = %d, want the 7 that was written", imported.Status.Usage.OutputTokens)
	}
	if len(imported.PhaseAt) != 2 {
		t.Errorf("imported phase_at holds %d entries, want submitted and done", len(imported.PhaseAt))
	}

	// rate_card is the third column the submit statement binds; read it back
	// as text so a hex literal could not pass for an object.
	var card string
	if err := serving.QueryRow(ctx, `SELECT rate_card::text FROM jobs WHERE id = $1`, j.ID).Scan(&card); err != nil {
		t.Fatalf("read back rate_card: %v", err)
	}
	if !strings.HasPrefix(card, "{") {
		t.Errorf("rate_card = %s, want a json object", card)
	}
}
