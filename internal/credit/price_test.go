// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/provider"
)

// A published rate card, in micro-USD per million tokens: $3 in, $15 out, $0.30
// cached, $3.75 to write a 5-minute entry and $6 to write an hour's.
var opus = Rates{
	Model:        "claude-opus-5",
	Input:        3 * Dollar,
	Output:       15 * Dollar,
	CacheRead:    300_000,
	CacheWrite5m: 3_750_000,
	CacheWrite1h: 6 * Dollar,
}

func TestProviderQualifiedRatesNeverCrossVendors(t *testing.T) {
	card := Card{Models: []Rates{
		{Model: "shared", Ref: managed.ModelRef{Provider: "anthropic", ID: "shared"}, Input: 3},
		{Model: "shared", Ref: managed.ModelRef{Provider: "openai", ID: "shared"}, Input: 9},
	}}
	anthropic, err := card.ForRef(managed.ModelRef{Provider: "anthropic", ID: "shared"})
	if err != nil || anthropic.Input != 3 {
		t.Fatalf("Anthropic rates = %+v, %v", anthropic, err)
	}
	openai, err := card.ForRef(managed.ModelRef{Provider: "openai", ID: "shared"})
	if err != nil || openai.Input != 9 {
		t.Fatalf("OpenAI rates = %+v, %v", openai, err)
	}
	if _, err := card.ForRef(managed.ModelRef{Provider: "latere", ID: "shared"}); !errors.Is(err, ErrNoRates) {
		t.Fatalf("uncovered provider = %v, want ErrNoRates", err)
	}
}

func TestManagedUsagePricesGenericAndExtraMetersOnce(t *testing.T) {
	ref := managed.ModelRef{Provider: "openai", ID: "gpt-test"}
	rates := Rates{
		Model: ref.ID, Ref: ref, Input: 1_000_000, Output: 2_000_000, CacheRead: 100_000,
		CacheWrite: 500_000, Extra: []MeterRate{{Meter: "container_seconds", Rate: 3_000_000}},
	}
	usage := managed.Usage{
		Model: ref, InputFresh: 1, InputCached: 2, CacheWrite: 3, Output: 4,
		Extra: map[managed.Meter]int64{"container_seconds": 5},
	}
	got, err := rates.PriceManaged(usage)
	if err != nil || got != 26 {
		t.Fatalf("PriceManaged = %d, %v; want 26", got, err)
	}
	usage.Extra["unpriced"] = 1
	if _, err := rates.PriceManaged(usage); err == nil {
		t.Fatal("unpriced reported meter was silently ignored")
	}
}

func TestLegacyRateJSONBackfillsAnthropicWithoutChangingPrice(t *testing.T) {
	raw := []byte(`{"models":[{"model":"claude-old","input":3000000,"output":15000000,"cache_read":300000,"cache_write_5m":3750000,"cache_write_1h":6000000}]}`)
	var card Card
	if err := json.Unmarshal(raw, &card); err != nil {
		t.Fatalf("decode: %v", err)
	}
	rates, err := card.ForRef(managed.ModelRef{Provider: "anthropic", ID: "claude-old"})
	if err != nil || rates.Ref.Provider != "anthropic" {
		t.Fatalf("legacy rates = %+v, %v", rates, err)
	}
	legacyUsage := provider.Usage{InputTokens: 10, OutputTokens: 2, CacheWriteTokens: 1}
	if got, want := rates.Price(legacyUsage), int64(66); got != want {
		t.Fatalf("migrated price = %d, want %d", got, want)
	}
}

func TestExplicitMemoryRateIsFreeButDoesNotCoverUnknownModels(t *testing.T) {
	card := DefaultCard()
	rates, err := card.ForRef(managed.ModelRef{Provider: "memory", ID: "memory-model"})
	if err != nil || !rates.Free {
		t.Fatalf("memory rates = %+v, %v", rates, err)
	}
	if _, err := card.ForRef(managed.ModelRef{Provider: "memory", ID: "unknown"}); !errors.Is(err, ErrNoRates) {
		t.Fatalf("unknown memory model = %v", err)
	}
}

func TestManagedPricingRejectsMismatchedAndNegativeUsage(t *testing.T) {
	ref := managed.ModelRef{Provider: "openai", ID: "gpt"}
	rates := Rates{Model: ref.ID, Ref: ref, Input: 1, Extra: []MeterRate{{Meter: "calls", Rate: 1}}}
	for _, usage := range []managed.Usage{
		{Model: managed.ModelRef{Provider: "anthropic", ID: "gpt"}, InputFresh: 1},
		{Model: ref, InputFresh: -1},
		{Model: ref, Extra: map[managed.Meter]int64{"calls": -1}},
	} {
		if _, err := rates.PriceManaged(usage); err == nil {
			t.Fatalf("invalid usage accepted: %+v", usage)
		}
	}
}

func TestProviderFallbackSynthesizesDearerExtraMeters(t *testing.T) {
	providerName := "openai"
	card := Card{Models: []Rates{
		{Model: "a", Ref: managed.ModelRef{Provider: providerName, ID: "a"}, Input: 2, Extra: []MeterRate{{Meter: "calls", Rate: 3}}},
		{Model: "b", Ref: managed.ModelRef{Provider: providerName, ID: "b"}, Input: 1, Extra: []MeterRate{{Meter: "calls", Rate: 5}, {Meter: "seconds", Rate: 7}}},
	}}
	rates, err := card.ForRef(managed.ModelRef{Provider: providerName, ID: "new"})
	if err != nil {
		t.Fatalf("ForRef: %v", err)
	}
	if calls, _ := rates.extraRate("calls"); rates.Input != 2 || calls != 5 {
		t.Fatalf("fallback = %+v", rates)
	}
	if seconds, ok := rates.extraRate("seconds"); !ok || seconds != 7 {
		t.Fatalf("seconds = %d, %v", seconds, ok)
	}
	if _, ok := card.Priced("new"); ok {
		t.Fatal("fallback was reported as explicitly priced")
	}
}

func TestRateJSONValidationAndRefOnlyCompatibility(t *testing.T) {
	var rates Rates
	if err := json.Unmarshal([]byte(`not-json`), &rates); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	if err := json.Unmarshal([]byte(`{"model_ref":{"provider":"openai","id":"gpt"},"input":1}`), &rates); err != nil {
		t.Fatalf("ref-only decode: %v", err)
	}
	if rates.Model != "gpt" || rates.Ref.Provider != "openai" {
		t.Fatalf("ref-only rates = %+v", rates)
	}
}

var haiku = Rates{
	Model:        "claude-haiku-4-5",
	Input:        1 * Dollar,
	Output:       5 * Dollar,
	CacheRead:    100_000,
	CacheWrite5m: 1_250_000,
	CacheWrite1h: 2 * Dollar,
}

// Every class is priced at its own rate. Collapsing them into one input rate
// mis-charges a long agent session by more than the session costs: a real run
// is overwhelmingly cache reads, which are a tenth of fresh input, and its
// cache writes are more than fresh input.
func TestPriceChargesEachClassAtItsOwnRate(t *testing.T) {
	u := provider.Usage{
		InputTokens:        1_000_000,
		OutputTokens:       100_000,
		CachedInputTokens:  10_000_000,
		CacheWrite5mTokens: 400_000,
		CacheWrite1hTokens: 200_000,
	}
	// 3.00 + 1.50 + 3.00 + 1.50 + 1.20 = $10.20
	if got, want := opus.Price(u), int64(10_200_000); got != want {
		t.Errorf("price = %d, want %d", got, want)
	}
	// The classes really are independent: doubling only the cache reads moves
	// the bill by exactly the cache-read rate.
	u.CachedInputTokens *= 2
	if got, want := opus.Price(u), int64(13_200_000); got != want {
		t.Errorf("price with doubled cache reads = %d, want %d", got, want)
	}
}

// A cache write whose lifetime the backend did not report — what a per-request
// span carries — is charged at the dearer of the two lifetimes. Guessing cheap
// would turn an unreported field into a discount nobody granted.
func TestPriceChargesAnUnattributedCacheWriteAtTheDearerRate(t *testing.T) {
	u := provider.Usage{CacheWriteTokens: 1_000_000}
	if got, want := opus.Price(u), opus.CacheWrite1h; got != want {
		t.Errorf("unattributed write priced at %d, want the 1h rate %d", got, want)
	}
	// And it is the dearer of the two whichever way round they are.
	cheap1h := Rates{CacheWrite5m: 9 * Dollar, CacheWrite1h: 1 * Dollar}
	if got, want := cheap1h.Price(u), int64(9*Dollar); got != want {
		t.Errorf("unattributed write priced at %d, want %d", got, want)
	}
}

// Rounding goes against the platform, by one micro-dollar at most. The opposite
// bias, repeated over every session, is the platform quietly paying.
func TestPriceRoundsUpRatherThanAway(t *testing.T) {
	// One token at $3/Mtok is 3 micro-USD exactly.
	if got := opus.Price(provider.Usage{InputTokens: 1}); got != 3 {
		t.Errorf("one input token = %d, want 3", got)
	}
	// A single token at a rate that does not divide evenly still costs something.
	odd := Rates{Input: 1}
	if got := odd.Price(provider.Usage{InputTokens: 1}); got != 1 {
		t.Errorf("a sub-micro charge = %d, want 1 — it must not round to free", got)
	}
	if got := opus.Price(provider.Usage{}); got != 0 {
		t.Errorf("no usage = %d, want 0", got)
	}
}

// An exact match wins; anything else is charged at the dearest rates on the
// card, class by class. A vendor shipping a model id we have not priced must
// not become free compute, and must not become an outage either.
func TestForFailsExpensiveOnAnUnknownModel(t *testing.T) {
	card := Card{Models: []Rates{haiku, opus}}

	got, err := card.For("claude-haiku-4-5")
	if err != nil || !reflect.DeepEqual(got, haiku) {
		t.Fatalf("exact match = %+v, %v; want haiku", got, err)
	}

	unknown, err := card.For("claude-something-6")
	if err != nil {
		t.Fatalf("unknown model: %v", err)
	}
	if unknown.Model != "claude-something-6" {
		t.Errorf("snapshot names %q; it must record what was asked for", unknown.Model)
	}
	if unknown.Input != opus.Input || unknown.Output != opus.Output ||
		unknown.CacheRead != opus.CacheRead || unknown.CacheWrite1h != opus.CacheWrite1h {
		t.Errorf("unknown model priced at %+v, want the dearest of each class", unknown)
	}
	// Dearest is per class, not "the dearest model": a card whose cheapest model
	// happens to have the priciest cache still cannot be undercharged.
	odd := Card{Models: []Rates{
		{Model: "a", Input: 1, Output: 9, CacheRead: 1, CacheWrite5m: 1, CacheWrite1h: 1},
		{Model: "b", Input: 9, Output: 1, CacheRead: 9, CacheWrite5m: 9, CacheWrite1h: 9},
	}}
	dear, _ := odd.For("c")
	if dear.Input != 9 || dear.Output != 9 {
		t.Errorf("per-class dearest = %+v, want 9 for both", dear)
	}
}

// Fail-closed: tokens nobody can price are not free tokens, so a deployment
// with no card cannot start a session at all.
func TestForRefusesWithNoCard(t *testing.T) {
	if _, err := (Card{}).For("claude-opus-5"); !errors.Is(err, ErrNoRates) {
		t.Errorf("empty card: want ErrNoRates, got %v", err)
	}
	// A card holding only a free row is an empty card: a zero row would price a
	// session at nothing, which is the one answer that must never be reached by
	// accident.
	free := Card{Models: []Rates{{Model: "claude-opus-5"}}}
	if _, err := free.For("claude-opus-5"); !errors.Is(err, ErrNoRates) {
		t.Errorf("card of zero rates: want ErrNoRates, got %v", err)
	}
}

// A dated snapshot is the model it is a snapshot of, and must be billed as one.
//
// This is not hypothetical: the first production boot of 011 resolved the
// "fast" preference to claude-haiku-4-5-20251001 against a card naming
// claude-haiku-4-5, and charged it the dearest rates on the card — $5/$25
// instead of $1/$5, five times over, on the tier chosen for being cheap.
func TestASnapshotIsPricedAsTheModelItIs(t *testing.T) {
	card := Card{Models: []Rates{haiku, opus}}

	got, err := card.For("claude-haiku-4-5-20251001")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if got.Input != haiku.Input || got.Output != haiku.Output {
		t.Errorf("a dated haiku was priced at %d/%d, want haiku's %d/%d", got.Input, got.Output, haiku.Input, haiku.Output)
	}
	// The snapshot still records what was asked for, so an amount can be traced
	// back to the id that produced it.
	if got.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("the entry names %q; it must record what was asked for", got.Model)
	}
	// An exact match is still exact, and still wins.
	if exact, _ := card.For("claude-haiku-4-5"); exact.Model != "claude-haiku-4-5" {
		t.Errorf("an exact match was rewritten: %+v", exact)
	}
}

// The suffix has to be a date and nothing else. A bare prefix test would be
// wrong in the expensive direction and look correct doing it: a card naming a
// shorter id would swallow a longer, different, differently-priced model.
func TestASnapshotMatchDoesNotSwallowADifferentModel(t *testing.T) {
	// `claude-opus-4` is a prefix of `claude-opus-4-8`, which is another model.
	card := Card{Models: []Rates{
		{Model: "claude-opus-4", Input: 1 * Dollar, Output: 2 * Dollar, CacheRead: 1, CacheWrite5m: 1, CacheWrite1h: 1},
		{Model: "expensive", Input: 90 * Dollar, Output: 90 * Dollar, CacheRead: 9, CacheWrite5m: 9, CacheWrite1h: 9},
	}}
	got, err := card.For("claude-opus-4-8")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if got.Input == 1*Dollar {
		t.Error("a different model was priced as a snapshot of a shorter id")
	}
	if got.Input != 90*Dollar {
		t.Errorf("input = %d, want the dearest rate %d — an unrecognised model fails expensive", got.Input, 90*Dollar)
	}

	// Nor does a version fragment count as a date.
	for _, id := range []string{"claude-opus-4-8", "claude-opus-4-x", "claude-opus-4-", "claude-opus-4-2026"} {
		if isSnapshotOf("claude-opus-4", id) {
			t.Errorf("%q was treated as a dated snapshot", id)
		}
	}
	for _, id := range []string{"claude-opus-4-20260901", "claude-opus-4-251001"} {
		if !isSnapshotOf("claude-opus-4", id) {
			t.Errorf("%q was not treated as a dated snapshot", id)
		}
	}
}
