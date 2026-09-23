// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/paper"
	"github.com/latere-ai/replichai/internal/platform"
)

// stubResolver stands in for a backend catalog and counts how often it is
// called, so the memoisation can be observed.
type stubResolver struct {
	calls atomic.Int64
	fail  bool
}

func (s *stubResolver) ResolveModel(_ context.Context, pref string) (string, error) {
	s.calls.Add(1)
	if s.fail {
		return "", errors.New("backend unreachable")
	}
	switch pref {
	case "best":
		return "claude-opus-5", nil
	case "balanced":
		return "claude-sonnet-5", nil
	default:
		return "claude-haiku-4-5-20251001", nil
	}
}

// stubPolicy serves one platform settings row.
type stubPolicy struct {
	set platform.Settings
	err error
}

func (s stubPolicy) Settings(context.Context) (platform.Settings, error) {
	return s.set, s.err
}

func tierDeps(resolver credit.ModelResolver) Deps {
	d := testDeps(allowAll)
	d.Platform = stubPolicy{set: platform.Settings{Rates: credit.DefaultCard()}}
	d.Models = resolver
	return d
}

func readTiers(t *testing.T, h http.Handler) []tierPrice {
	t.Helper()
	w := do(t, h, "GET", "/models", "", "sub-1")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /models = %d, want 200", w.Code)
	}
	var got struct {
		Tiers []tierPrice `json:"tiers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got.Tiers
}

// TestModelsPricesEveryTierFromTheCard checks that the picker's prices come
// from the server. The card is the platform's policy, and a second copy of it
// in the client would be a second answer to what a run costs, which the
// snapshot in 011 exists to prevent.
func TestModelsPricesEveryTierFromTheCard(t *testing.T) {
	h := NewRouter(tierDeps(&stubResolver{})).Handler()

	tiers := readTiers(t, h)
	if len(tiers) != len(credit.Preferences) {
		t.Fatalf("listed %d tiers, want %d", len(tiers), len(credit.Preferences))
	}
	byTier := map[string]tierPrice{}
	for _, p := range tiers {
		byTier[p.Tier] = p
	}
	for _, want := range []struct {
		tier, model string
		in, out     int64
	}{
		{"best", "claude-opus-5", 5 * credit.Dollar, 25 * credit.Dollar},
		{"balanced", "claude-sonnet-5", 3 * credit.Dollar, 15 * credit.Dollar},
		// A dated snapshot of a model the card names is priced as that model,
		// not at the dearest-rates fallback.
		{"fast", "claude-haiku-4-5-20251001", 1 * credit.Dollar, 5 * credit.Dollar},
	} {
		got := byTier[want.tier]
		if got.Model != want.model || got.Input != want.in || got.Output != want.out {
			t.Errorf("%s = %+v, want %s at %d/%d", want.tier, got, want.model, want.in, want.out)
		}
		if !got.Named {
			t.Errorf("%s resolved to %q and was reported unpriced by a card that covers it", want.tier, got.Model)
		}
	}
}

// TestModelsAreMemoised checks that the tier list is memoised. Resolving a
// tier is one backend round trip each, and a page that offers the choice must
// not make three of them per load.
func TestModelsAreMemoised(t *testing.T) {
	r := &stubResolver{}
	h := NewRouter(tierDeps(r)).Handler()

	readTiers(t, h)
	after := r.calls.Load()
	if after != int64(len(credit.Preferences)) {
		t.Fatalf("first read made %d resolutions, want one per tier (%d)", after, len(credit.Preferences))
	}
	readTiers(t, h)
	if got := r.calls.Load(); got != after {
		t.Errorf("second read made %d more resolutions; the list is memoised for %s", got-after, modelsTTL)
	}
}

// TestAFailedPricingIsNotCached checks that an unpriced answer is not cached.
// A backend that timed out once would otherwise serve blank prices to every
// caller for the whole TTL, turning one slow read into a five-minute outage.
func TestAFailedPricingIsNotCached(t *testing.T) {
	r := &stubResolver{fail: true}
	h := NewRouter(tierDeps(r)).Handler()

	if tiers := readTiers(t, h); tiers[0].Model != "" {
		t.Fatalf("expected an unpriced answer while the backend is failing, got %+v", tiers[0])
	}
	first := r.calls.Load()
	if first == 0 {
		t.Fatal("the failing backend was never asked")
	}

	// The backend recovers, and the next read must see it.
	r.fail = false
	tiers := readTiers(t, h)
	if r.calls.Load() == first {
		t.Fatal("a failed pricing was cached; the recovered backend is never asked again this TTL")
	}
	if tiers[0].Model != "claude-opus-5" || tiers[0].Input == 0 {
		t.Errorf("after recovery the tiers are still unpriced: %+v", tiers[0])
	}
}

// TestModelsListTiersWithoutABackend checks that a deployment that cannot
// price the tiers still offers them. Removing the control because a backend is
// unreachable would lose the choice as well as the price.
func TestModelsListTiersWithoutABackend(t *testing.T) {
	for _, tc := range []struct {
		name string
		deps func() Deps
	}{
		{"no resolver", func() Deps {
			d := testDeps(allowAll)
			d.Platform = stubPolicy{set: platform.Settings{Rates: credit.DefaultCard()}}
			return d
		}},
		{"no policy", func() Deps { d := testDeps(allowAll); d.Models = &stubResolver{}; return d }},
		{"policy unreadable", func() Deps {
			d := tierDeps(&stubResolver{})
			d.Platform = stubPolicy{err: errors.New("down")}
			return d
		}},
		{"backend unreachable", func() Deps { return tierDeps(&stubResolver{fail: true}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tiers := readTiers(t, NewRouter(tc.deps()).Handler())
			if len(tiers) != len(credit.Preferences) {
				t.Fatalf("listed %d tiers, want all %d offered", len(tiers), len(credit.Preferences))
			}
			for i, p := range tiers {
				if p.Tier != credit.Preferences[i].String() {
					t.Errorf("tier %d = %q, want %q", i, p.Tier, credit.Preferences[i])
				}
			}
		})
	}
}

// TestStartSessionCarriesTheChosenTier checks that the tier a person picks is
// the one the session is enqueued with.
func TestStartSessionCarriesTheChosenTier(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	const url = "https://arxiv.org/abs/2302.03714"

	_, projectID := createWithPaper(t, h, url, "sub-1")
	key, err := paper.Key(url)
	if err != nil {
		t.Fatalf("paper.Key: %v", err)
	}
	w := do(t, h, "POST", "/projects/"+projectID+"/sessions", `{"paper_key":"`+key+`","tier":"fast"}`, "sub-1")
	if w.Code != http.StatusCreated {
		t.Fatalf("start = %d: %s", w.Code, w.Body)
	}
	var got submitResult
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	j, err := d.Jobs.Get(context.Background(), got.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if j.Tier != credit.PrefFast {
		t.Errorf("session enqueued on tier %q, want %q", j.Tier, credit.PrefFast)
	}
}

// TestStartSessionRefusesAnUnknownTier checks that an unrecognised tier is
// refused before anything is written. The adapter passes a preference it does
// not know through as a literal model id, and a model the card does not name
// is billed at the dearest rates the card holds, so a typo here would become a
// bill.
func TestStartSessionRefusesAnUnknownTier(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	const url = "https://arxiv.org/abs/2302.03714"

	_, projectID := createWithPaper(t, h, url, "sub-1")
	key, err := paper.Key(url)
	if err != nil {
		t.Fatalf("paper.Key: %v", err)
	}
	for _, bad := range []string{"opus", "claude-opus-5", "cheapest", "Best"} {
		w := do(t, h, "POST", "/projects/"+projectID+"/sessions", `{"paper_key":"`+key+`","tier":"`+bad+`"}`, "sub-1")
		if w.Code != http.StatusBadRequest {
			t.Errorf("tier %q = %d, want 400", bad, w.Code)
		}
		var body struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		if body.Error != "bad_tier" {
			t.Errorf("tier %q refused as %q, want bad_tier", bad, body.Error)
		}
	}
	// Nothing was enqueued. A refusal at the boundary leaves no session and no
	// hold behind.
	jobs, err := d.Jobs.ListByProject(context.Background(), projectID)
	if err != nil {
		t.Fatalf("ListByProject: %v", err)
	}
	if len(jobs) != 0 {
		t.Errorf("%d sessions exist after refused starts; want none", len(jobs))
	}
}

// TestStartSessionWithoutATierIsUnset checks that omitting the tier is not an
// error. The caller did not choose, so the engine's default applies, which
// keeps every existing client working.
func TestStartSessionWithoutATierIsUnset(t *testing.T) {
	d := testDeps(allowAll)
	h := NewRouter(d).Handler()
	const url = "https://arxiv.org/abs/2302.03714"

	_, projectID := createWithPaper(t, h, url, "sub-1")
	key, _ := paper.Key(url)
	_, got := startPaper(t, h, projectID, key, "sub-1")
	j, err := d.Jobs.Get(context.Background(), got.JobID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if j.Tier != "" {
		t.Errorf("an unchosen tier was stored as %q", j.Tier)
	}
	if j.Tier.OrDefault() != credit.PrefBest {
		t.Errorf("an unchosen session resolves to %q, want %q", j.Tier.OrDefault(), credit.PrefBest)
	}
}
