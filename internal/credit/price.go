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
// An exact match wins. A **dated snapshot** of a model the card names is charged
// at that model's rates: a backend listing `claude-haiku-4-5-20251001` is
// offering Claude Haiku 4.5, and billing it as an unknown model would charge it
// the dearest rates on the card — five times its real price, on the tier chosen
// for being cheap. Anything else is charged at the dearest rates, class by
// class: never at zero, which would make a vendor's new model id free compute,
// and never by refusing to start, which would make it an outage. The synthesized
// entry keeps the requested model's name, so a snapshot records what was asked
// for and the amount records what it was charged as.
//
// An empty card cannot price anything and is ErrNoRates. That is fail-closed on
// purpose: tokens nobody can price are not free tokens.
func (c Card) For(model string) (Rates, error) {
	model = strings.TrimSpace(model)
	priced := make([]Rates, 0, len(c.Models))
	var snapshot *Rates
	for _, r := range c.Models {
		if r.Zero() {
			continue // a free row prices nothing; it cannot stand in for a model
		}
		name := strings.TrimSpace(r.Model)
		if strings.EqualFold(name, model) && model != "" {
			return r, nil
		}
		if snapshot == nil && isSnapshotOf(name, model) {
			hit := r
			hit.Model = model // record what was asked for, priced as what it is
			snapshot = &hit
		}
		priced = append(priced, r)
	}
	if snapshot != nil {
		return *snapshot, nil
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

// isSnapshotOf reports whether model is a dated snapshot of the card entry
// named alias — `claude-haiku-4-5-20251001` of `claude-haiku-4-5`.
//
// The suffix must be a date and nothing else. A bare prefix test would be
// wrong in a way that costs money silently: a card naming `claude-opus-4` would
// swallow `claude-opus-4-8`, a different model at a different price, and the
// mispricing would look exactly like a correct match.
func isSnapshotOf(alias, model string) bool {
	if alias == "" || len(model) <= len(alias)+1 {
		return false
	}
	if !strings.EqualFold(model[:len(alias)], alias) || model[len(alias)] != '-' {
		return false
	}
	suffix := model[len(alias)+1:]
	if len(suffix) < 6 { // a date, not a version fragment
		return false
	}
	for _, r := range suffix {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
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

// DefaultCard is the price list this deployment is seeded with: the published
// per-million-token rates for the models the engine's tier preference resolves
// to ("best" → opus, "balanced" → sonnet, "fast" → haiku).
//
// Cache rates are derived from the published multipliers rather than typed
// separately, so a card row cannot say a cache read costs more than fresh
// input: a read is a tenth of input, a 5-minute write is 1.25× it, and an
// hour's write is twice it.
//
// It is a *seed*, not the truth. Vendor prices move, and the platform_settings
// row is what a session is actually priced from — an admin edits it in the
// console and the change applies to sessions starting afterwards. A model the
// card does not name is charged at the dearest rates it holds (Card.For), which
// is why the list stops at the tiers this deployment runs: a model outside them
// is most likely a new variant of the dearest one already here.
func DefaultCard() Card {
	rates := func(model string, in, out int64) Rates {
		return Rates{
			Model:        model,
			Input:        in,
			Output:       out,
			CacheRead:    in / 10,
			CacheWrite5m: in + in/4,
			CacheWrite1h: in * 2,
		}
	}
	return Card{Models: []Rates{
		rates("claude-opus-5", 5*Dollar, 25*Dollar),
		rates("claude-sonnet-5", 3*Dollar, 15*Dollar),
		rates("claude-haiku-4-5", 1*Dollar, 5*Dollar),
	}}
}
