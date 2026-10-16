// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
)

// priceListPayload is a trimmed copy of OpenRouter's model list: two Anthropic
// models, one with a long-context tier, a billing variant, another vendor's
// model, and an Anthropic model missing its one-hour cache-write price.
const priceListPayload = `{"data":[
 {"id":"anthropic/claude-opus-5.5","pricing":{"prompt":"0.000004","completion":"0.00002","input_cache_read":"0.0000002","input_cache_write":"0.000005","input_cache_write_1h":"0.000008"}},
 {"id":"anthropic/claude-haiku-5.5","pricing":{"prompt":"0.0000001","completion":"0.0000005","input_cache_read":"0.00000001","input_cache_write":"0.000000125","input_cache_write_1h":"0.0000002",
  "overrides":[{"min_prompt_tokens":100000,"prompt":"0.0000005","completion":"0.0000025"}]}},
 {"id":"anthropic/claude-opus-5.5:batch","pricing":{"prompt":"0.000002","completion":"0.00001","input_cache_read":"0.0000001","input_cache_write":"0.0000025","input_cache_write_1h":"0.000004"}},
 {"id":"openai/gpt-6","pricing":{"prompt":"0.000003","completion":"0.000012","input_cache_read":"0.0000003","input_cache_write":"0.000003","input_cache_write_1h":"0.000003"}},
 {"id":"anthropic/claude-sonnet-9","pricing":{"prompt":"0.000002","completion":"0.00001","input_cache_read":"0.0000002","input_cache_write":"0.0000025"}}
]}`

func servePriceList(t *testing.T, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestFetchPriceListReadsAnthropicPrices checks that the list's Anthropic
// models come back under the ids Anthropic's API takes, at their exact prices
// and at the base tier, and that variants, other vendors and models missing a
// price are left out.
func TestFetchPriceListReadsAnthropicPrices(t *testing.T) {
	got, err := FetchPriceList(context.Background(), http.DefaultClient, servePriceList(t, http.StatusOK, priceListPayload))
	if err != nil {
		t.Fatalf("FetchPriceList: %v", err)
	}
	want := map[string]Rates{
		"claude-opus-5-5":  {Input: 4_000_000, Output: 20_000_000, CacheRead: 200_000, CacheWrite5m: 5_000_000, CacheWrite1h: 8_000_000},
		"claude-haiku-5-5": {Input: 100_000, Output: 500_000, CacheRead: 10_000, CacheWrite5m: 125_000, CacheWrite1h: 200_000},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d models %+v, want %d", len(got), got, len(want))
	}
	for _, r := range got {
		w, ok := want[r.Model]
		if !ok {
			t.Errorf("unexpected model %q", r.Model)
			continue
		}
		if r.Ref != (managed.ModelRef{Provider: "anthropic", ID: r.Model}) {
			t.Errorf("%s ref = %+v", r.Model, r.Ref)
		}
		if r.Input != w.Input || r.Output != w.Output || r.CacheRead != w.CacheRead || r.CacheWrite5m != w.CacheWrite5m || r.CacheWrite1h != w.CacheWrite1h {
			t.Errorf("%s = %+v, want %+v", r.Model, r, w)
		}
	}
}

// TestFetchPriceListRefusesAnOutage checks that a failed or empty list is an
// error rather than a list with no prices in it.
func TestFetchPriceListRefusesAnOutage(t *testing.T) {
	for name, url := range map[string]string{
		"status":       servePriceList(t, http.StatusServiceUnavailable, `{}`),
		"no anthropic": servePriceList(t, http.StatusOK, `{"data":[{"id":"openai/gpt-6","pricing":{"prompt":"0.000003","completion":"0.000012"}}]}`),
		"not json":     servePriceList(t, http.StatusOK, `<html>`),
	} {
		if _, err := FetchPriceList(context.Background(), http.DefaultClient, url); err == nil {
			t.Errorf("%s: FetchPriceList succeeded, want an error", name)
		}
	}
}

// TestPerMillionIsExact checks the conversion from USD per token to micro-USD
// per million tokens on values a float64 cannot hold exactly.
func TestPerMillionIsExact(t *testing.T) {
	for in, want := range map[string]int64{
		"0.000000125":     125_000,
		"0.000004":        4_000_000,
		"1.25e-7":         125_000,
		"0.0000000000005": 0, // rounds to zero: not a price
	} {
		got, ok := perMillion(in)
		if want == 0 {
			if ok {
				t.Errorf("perMillion(%q) = %d, want refused", in, got)
			}
			continue
		}
		if !ok || got != want {
			t.Errorf("perMillion(%q) = (%d, %v), want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "-0.000004", "abc", "1e999"} {
		if _, ok := perMillion(bad); ok {
			t.Errorf("perMillion(%q) accepted", bad)
		}
	}
}

// TestWithListedFollowsTheList checks the merge: a listed model's row takes the
// listed prices and keeps its meters, a row the list does not price is kept as
// written, a listed model the card lacks is appended in id order, and a second
// merge of the same list changes nothing.
func TestWithListedFollowsTheList(t *testing.T) {
	meter := MeterRate{Meter: "container_seconds", Rate: 7}
	card := Card{Models: []Rates{
		// A row written before rows carried a ref, at a stale price.
		{Model: "claude-opus-5-5", Input: 5_000_000, Output: 25_000_000, Extra: []MeterRate{meter}},
		{Model: "my-local-model", Ref: managed.ModelRef{Provider: "local", ID: "my-local-model"}, Input: 1, Output: 1},
	}}
	listed := []Rates{
		{Model: "claude-sonnet-5-5", Ref: managed.ModelRef{Provider: "anthropic", ID: "claude-sonnet-5-5"}, Input: 2_000_000, Output: 10_000_000, CacheRead: 100_000, CacheWrite5m: 2_500_000, CacheWrite1h: 4_000_000},
		{Model: "claude-opus-5-5", Ref: managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5-5"}, Input: 4_000_000, Output: 20_000_000, CacheRead: 200_000, CacheWrite5m: 5_000_000, CacheWrite1h: 8_000_000},
		{Model: "claude-haiku-5-5", Ref: managed.ModelRef{Provider: "anthropic", ID: "claude-haiku-5-5"}, Input: 100_000, Output: 500_000, CacheRead: 10_000, CacheWrite5m: 125_000, CacheWrite1h: 200_000},
	}

	next, changed := card.WithListed(listed)
	if !changed {
		t.Fatal("a stale card reported no change")
	}
	if err := next.Validate(); err != nil {
		t.Fatalf("merged card does not validate: %v", err)
	}
	var models []string
	for _, r := range next.Models {
		models = append(models, r.Model)
	}
	if want := []string{"claude-opus-5-5", "my-local-model", "claude-haiku-5-5", "claude-sonnet-5-5"}; !slices.Equal(models, want) {
		t.Fatalf("rows = %v, want %v", models, want)
	}
	opus := next.Models[0]
	if opus.Input != 4_000_000 || opus.Output != 20_000_000 || opus.CacheWrite1h != 8_000_000 {
		t.Errorf("opus = %+v, want the listed prices", opus)
	}
	if len(opus.Extra) != 1 || opus.Extra[0] != meter {
		t.Errorf("opus meters = %+v, want its own meter kept", opus.Extra)
	}
	if next.Models[1].Input != 1 {
		t.Errorf("an unlisted row was changed: %+v", next.Models[1])
	}
	if _, again := next.WithListed(listed); again {
		t.Error("merging the same list twice reported a change")
	}
}
