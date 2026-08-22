package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/credit"
)

// A backend offering a model the card cannot price on its own terms — the case
// this command exists to catch. A *dated snapshot* of a named model is not that
// case (the card prices it as the model it is); a genuinely unfamiliar id is.
func TestRatesReportsAPreferenceTheCardDoesNotName(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
			{"id": "claude-opus-6-preview"},
			{"id": "claude-sonnet-5"},
			{"id": "claude-haiku-4-5"},
		}})
	}))
	defer backend.Close()
	t.Setenv("ANTHROPIC_API_KEY", "test-credential")
	t.Setenv("ANTHROPIC_BASE_URL", backend.URL)

	out := runRatesJSON(t, "-runtime", "anthropic-managed", "-json")
	byPref := map[credit.Preference]credit.Coverage{}
	for _, c := range out.Checks {
		byPref[c.Preference] = c
	}

	// "best" resolved to an id the card does not carry, so it is billed at the
	// dearest rates — never free, which is the design, but not opus's own price
	// either. That is the whole finding.
	best := byPref["best"]
	if best.Model != "claude-opus-6-preview" {
		t.Fatalf("best resolved to %q", best.Model)
	}
	if best.Named {
		t.Error("best reported an exact match against a card that does not name that id")
	}

	// The two the card does name are exact, so a reader can tell the one
	// finding apart from the noise.
	for _, pref := range []credit.Preference{credit.PrefBalanced, credit.PrefFast} {
		if c := byPref[pref]; !c.Named {
			t.Errorf("%s resolved to %q and was reported inexact against a card that names it", pref, c.Model)
		}
	}
}

// When every preference resolves to a model the card names, the command says so
// with nothing to act on — the case an operator wants to see before a release.
func TestRatesReportsACardThatCoversTheBackend(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
			{"id": "claude-opus-5"}, {"id": "claude-sonnet-5"}, {"id": "claude-haiku-4-5"},
		}})
	}))
	defer backend.Close()
	t.Setenv("ANTHROPIC_API_KEY", "test-credential")
	t.Setenv("ANTHROPIC_BASE_URL", backend.URL)

	out := runRatesJSON(t, "-runtime", "anthropic-managed", "-json")
	for _, c := range out.Checks {
		if !c.Named {
			t.Errorf("%s (%s) reported inexact against a card that names it", c.Preference, c.Model)
		}
		if c.Rates.Input <= 0 || c.Rates.Output <= 0 {
			t.Errorf("%s priced at %d/%d — a zero rate would make sessions free", c.Preference, c.Rates.Input, c.Rates.Output)
		}
	}
	if out.Source != "compiled-in seed" {
		t.Errorf("source = %q; with no database it must say which card it read", out.Source)
	}
}

type ratesReport struct {
	Source string            `json:"source"`
	Checks []credit.Coverage `json:"checks"`
}

func runRatesJSON(t *testing.T, args ...string) ratesReport {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "rates-*.json")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	defer f.Close()
	if err := runRates(context.Background(), args, f, os.Stderr); err != nil {
		t.Fatalf("runRates: %v", err)
	}
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var out ratesReport
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode %s: %v", strings.TrimSpace(string(raw)), err)
	}
	return out
}
