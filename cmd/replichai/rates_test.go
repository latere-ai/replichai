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
	"github.com/latere-ai/replichai/internal/provider"
	"github.com/latere-ai/replichai/internal/provider/anthropic"
)

// A backend whose model list is a dated snapshot rather than the bare alias —
// which is the case this command exists to catch. `ResolveModel` matches on the
// tier substring, so "best" resolves to the dated opus id, and the seeded card
// (which names `claude-opus-5`) has no entry for it.
func TestRatesReportsAPreferenceTheCardDoesNotName(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
			{"id": "claude-opus-5-20260901"},
			{"id": "claude-sonnet-5"},
			{"id": "claude-haiku-4-5"},
		}})
	}))
	defer backend.Close()
	registerStub(t, "rates-dated", backend.URL)

	out := runRatesJSON(t, "-provider", "rates-dated", "-json")
	byPref := map[string]rateCheck{}
	for _, c := range out.Checks {
		byPref[c.Preference] = c
	}

	// "best" resolved to an id the card does not carry, so it is billed at the
	// dearest rates — never free, which is the design, but not opus's own price
	// either. That is the whole finding.
	best := byPref["best"]
	if best.Model != "claude-opus-5-20260901" {
		t.Fatalf("best resolved to %q", best.Model)
	}
	if best.Exact {
		t.Error("best reported an exact match against a card that does not name that id")
	}

	// The two the card does name are exact, so a reader can tell the one
	// finding apart from the noise.
	for _, pref := range []string{"balanced", "fast"} {
		if c := byPref[pref]; !c.Exact {
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
	registerStub(t, "rates-exact", backend.URL)

	out := runRatesJSON(t, "-provider", "rates-exact", "-json")
	for _, c := range out.Checks {
		if !c.Exact {
			t.Errorf("%s (%s) reported inexact against a card that names it", c.Preference, c.Model)
		}
		if c.InputPerMTok <= 0 || c.OutputPerMTok <= 0 {
			t.Errorf("%s priced at %d/%d — a zero rate would make sessions free", c.Preference, c.InputPerMTok, c.OutputPerMTok)
		}
	}
	if out.Source != "compiled-in seed" {
		t.Errorf("source = %q; with no database it must say which card it read", out.Source)
	}
}

// isDearest is what separates "the card names this model" from "Card.For
// synthesised an entry carrying its name". The synthesised entry looks
// identical by name, which is exactly why the report cannot use the name.
func TestIsDearestSeesThroughTheSynthesisedName(t *testing.T) {
	card := credit.DefaultCard()
	if isDearest(card, "claude-opus-5") {
		t.Error("a model the card names was reported as priced by the fallback")
	}
	if !isDearest(card, "claude-opus-5-20260901") {
		t.Error("a model the card does not name was reported as having its own entry")
	}
	rates, err := card.For("claude-opus-5-20260901")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if rates.Model != "claude-opus-5-20260901" {
		t.Fatalf("the synthesised entry did not carry the requested name: %q", rates.Model)
	}
}

// registerStub wires the real anthropic adapter at a local backend under a
// one-off provider name, so the test drives the same ResolveModel the engine
// does rather than a fake of it.
func registerStub(t *testing.T, name, baseURL string) {
	t.Helper()
	provider.Register(name, func() (provider.Provider, error) {
		return anthropic.New(anthropic.WithAPIKey("test-credential"), anthropic.WithAPIBase(baseURL))
	})
}

type ratesReport struct {
	Source string      `json:"source"`
	Checks []rateCheck `json:"checks"`
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
