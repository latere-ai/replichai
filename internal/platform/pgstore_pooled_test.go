// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package platform

import (
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/draft"
)

// TestSetSettingsUnderExecMode pins how the platform row's two jsonb columns
// reach the database.
//
// A Go byte slice is sent as bytea. When the driver knows the parameter's
// type it encodes correctly, but when parameters travel in the text format the
// value arrives as a hex literal and `\x7b22...`::jsonb fails with SQLSTATE
// 22P02. The deployment's pooled DSN carries cache_describe, which learns the types
// from the server, so this pins the stricter default_query_exec_mode=exec: it
// is the only setting that makes the Go type behind a jsonb parameter
// observable, so passing under it proves the binding is right under any mode.
//
// Migrations run on the direct endpoint, as they do in the deployment.
func TestSetSettingsUnderExecMode(t *testing.T) {
	direct := testPool(t) // creates and migrates the package's own database
	defer direct.Close()

	base := os.Getenv("TEST_DATABASE_URL")
	dsn := swapDBName(t, base, platformTestDB)
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	serving, err := pgxpool.New(ctx, dsn+sep+"default_query_exec_mode=exec")
	if err != nil {
		t.Fatalf("connect to the serving endpoint: %v", err)
	}
	defer serving.Close()

	s := NewPGStore(serving, newAuthority(t))
	// rate_card and draft are the two jsonb parameters the upsert binds. The
	// rate card is left to the default so the store fills it in, and the
	// drafting model is set so the second column carries a real object.
	if err := s.SetSettings(ctx, admin, Settings{
		StartReserve: 7 * credit.Dollar,
		Draft:        draft.Config{Provider: "openai", BaseURL: "https://models.example/v1", Model: "small-1"},
	}); err != nil {
		t.Fatalf("SetSettings (insert): %v", err)
	}
	// A second write takes the ON CONFLICT arm, which binds the same two
	// parameters through EXCLUDED.
	if err := s.SetSettings(ctx, admin, Settings{
		StartReserve: 9 * credit.Dollar,
		Draft:        draft.Config{Provider: "openai", BaseURL: "https://models.example/v1", Model: "small-2"},
	}); err != nil {
		t.Fatalf("SetSettings (upsert): %v", err)
	}

	got, err := s.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if got.StartReserve != 9*credit.Dollar {
		t.Errorf("reserve = %d, want the %d that was written", got.StartReserve, 9*credit.Dollar)
	}
	if got.Draft.Model != "small-2" {
		t.Errorf("drafting model = %q, want small-2", got.Draft.Model)
	}
	if len(got.Rates.Models) == 0 {
		t.Error("rate_card came back empty; the default card was not stored")
	}

	// Read both columns back as text, so a hex literal could not pass for an
	// object.
	var card, drafting string
	if err := serving.QueryRow(ctx,
		`SELECT rate_card::text, draft::text FROM platform_settings WHERE id = 1`,
	).Scan(&card, &drafting); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.HasPrefix(card, "{") || !strings.HasPrefix(drafting, "{") {
		t.Errorf("rate_card = %s, draft = %s, want two json objects", card, drafting)
	}
}
