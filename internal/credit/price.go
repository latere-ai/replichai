package credit

import (
	"strings"

	"github.com/latere-ai/replichai/internal/provider"
)

// Rates price one model's tokens, in micro-USD per million tokens — the unit
// vendors quote, so an admin copying a price list types the number they read.
//
// The classes are separate because they are billed separately, and by a wide
// margin: a cache read is a fraction of fresh input and a cache write is a
// multiple of it. Collapsing them into one "input" rate would misprice a long
// agent session by more than it costs.
type Rates struct {
	// Model is the model these rates are for, recorded so a snapshot taken at
	// enqueue says what it priced.
	Model string `json:"model"`

	Input        int64 `json:"input"`
	Output       int64 `json:"output"`
	CacheRead    int64 `json:"cache_read"`
	CacheWrite5m int64 `json:"cache_write_5m"`
	CacheWrite1h int64 `json:"cache_write_1h"`
}

// Price is what a run's usage costs under these rates, in micro-USD.
//
// It is a pure function of (usage, rates) with no I/O, which is what lets it be
// called inside the transaction that settles a session and inside the watchdog
// that runs every poll. The rates come from a snapshot taken when the session
// started (011), so an admin editing the card mid-run cannot make the ceiling a
// run was measured against and the amount it is finally charged disagree.
func (r Rates) Price(u provider.Usage) int64 {
	// One division at the end rather than six: dividing per class would round
	// each of them away and systematically under-charge.
	perMillion := u.InputTokens*r.Input +
		u.OutputTokens*r.Output +
		u.CachedInputTokens*r.CacheRead +
		u.CacheWrite5mTokens*r.CacheWrite5m +
		u.CacheWrite1hTokens*r.CacheWrite1h +
		// A cache write whose lifetime the backend did not report is charged at
		// the dearer of the two, on the same fail-expensive rule an unknown
		// model follows. Guessing cheap would make an unreported lifetime a
		// discount nobody granted.
		u.CacheWriteTokens*max(r.CacheWrite5m, r.CacheWrite1h)
	return ceilDiv(perMillion, 1_000_000)
}

// Zero reports rates that price everything at nothing — a card entry that would
// make a session free. It is not a valid snapshot: the gate refuses to start a
// session it cannot price.
func (r Rates) Zero() bool {
	return r.Input == 0 && r.Output == 0 && r.CacheRead == 0 && r.CacheWrite5m == 0 && r.CacheWrite1h == 0
}

// Card is the platform's price list: what each model costs. An admin edits it,
// because a vendor's prices move and a constant compiled into the binary makes
// every price change a redeploy and a silent mispricing until then.
type Card struct {
	Models []Rates `json:"models"`
}

// For resolves the rates to charge a model at.
//
// An exact match wins. A model the card does not name is charged at the
// **dearest** rates the card holds, class by class — never at zero, which would
// make a vendor's new model id free compute, and never by refusing to start,
// which would make it an outage. The synthesized entry keeps the requested
// model's name, so a snapshot records what was asked for and the amount records
// what it was charged as.
//
// An empty card cannot price anything and is ErrNoRates. That is fail-closed on
// purpose: tokens nobody can price are not free tokens.
func (c Card) For(model string) (Rates, error) {
	model = strings.TrimSpace(model)
	priced := make([]Rates, 0, len(c.Models))
	for _, r := range c.Models {
		if r.Zero() {
			continue // a free row prices nothing; it cannot stand in for a model
		}
		if strings.EqualFold(strings.TrimSpace(r.Model), model) && model != "" {
			return r, nil
		}
		priced = append(priced, r)
	}
	if len(priced) == 0 {
		return Rates{}, ErrNoRates
	}
	dearest := Rates{Model: model}
	for _, r := range priced {
		dearest.Input = max(dearest.Input, r.Input)
		dearest.Output = max(dearest.Output, r.Output)
		dearest.CacheRead = max(dearest.CacheRead, r.CacheRead)
		dearest.CacheWrite5m = max(dearest.CacheWrite5m, r.CacheWrite5m)
		dearest.CacheWrite1h = max(dearest.CacheWrite1h, r.CacheWrite1h)
	}
	return dearest, nil
}

// ceilDiv divides rounding away from zero, so a fraction of a micro-USD is
// charged rather than dropped. The bias is deliberate and it is one millionth
// of a dollar per session; the opposite bias, repeated, is the platform paying.
func ceilDiv(n, d int64) int64 {
	if n <= 0 {
		return 0
	}
	return (n + d - 1) / d
}
