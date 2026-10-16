// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/latere-ai/replichai/internal/managed"
	"github.com/latere-ai/replichai/internal/provider"
)

// Rates price one model's tokens, in micro-USD per million tokens. That is the
// unit vendors quote, so an admin copying a price list types the number they
// read.
//
// The classes are separate because they are billed separately, and by a wide
// margin: a cache read is a fraction of fresh input and a cache write is a
// multiple of it. Collapsing them into one input rate would misprice a long
// agent session by more than it costs.
type Rates struct {
	// Model is the model these rates are for, recorded so a snapshot taken at
	// enqueue says what it priced.
	Model string `json:"model"`
	// Ref is the durable provider-qualified identity. Model remains in the JSON
	// for backward compatibility with snapshotted cards written before 041d.
	Ref managed.ModelRef `json:"model_ref"`

	Input        int64       `json:"input"`
	Output       int64       `json:"output"`
	CacheRead    int64       `json:"cache_read"`
	CacheWrite5m int64       `json:"cache_write_5m"`
	CacheWrite1h int64       `json:"cache_write_1h"`
	CacheWrite   int64       `json:"cache_write,omitempty"`
	Extra        []MeterRate `json:"extra,omitempty"`
	// Free is explicit; an all-zero row without it is treated as invalid.
	Free bool `json:"free,omitempty"`
}

// MeterRate prices one non-token meter, such as a container second or a hosted
// tool call, in the same micro-USD per million units the token rates use, so
// the whole bill is still computed with one division.
//
// A meter the card holds no entry for is unpriced rather than free:
// Rates.PriceManaged refuses usage that reports it, because a runtime billing
// on a meter the card does not list means the card is out of date, not that
// the session costs nothing.
type MeterRate struct {
	Meter managed.Meter `json:"meter"`
	Rate  int64         `json:"rate"`
}

// UnmarshalJSON assigns historical model-only rows to Anthropic explicitly. An
// empty provider never means the currently configured provider.
func (r *Rates) UnmarshalJSON(data []byte) error {
	type wire Rates
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = Rates(decoded)
	if !r.Ref.Valid() && strings.TrimSpace(r.Model) != "" {
		r.Ref = managed.ModelRef{Provider: "anthropic", ID: strings.TrimSpace(r.Model)}
	}
	if r.Model == "" && r.Ref.Valid() {
		r.Model = r.Ref.ID
	}
	return nil
}

// Price returns what a run's usage costs under these rates, in micro-USD.
//
// It is a pure function of usage and rates with no I/O, so it can be called
// inside the transaction that settles a session and inside the backstop that
// runs on every poll. The rates come from a snapshot taken when the session
// started (011), so an admin editing the card mid-run cannot make the ceiling
// a run was measured against and the amount it is finally charged disagree.
func (r Rates) Price(u provider.Usage) int64 {
	// One division at the end rather than six: dividing per class would round
	// each class down and systematically undercharge.
	perMillion := u.InputTokens*r.Input +
		u.OutputTokens*r.Output +
		u.CachedInputTokens*r.CacheRead +
		u.CacheWrite5mTokens*r.CacheWrite5m +
		u.CacheWrite1hTokens*r.CacheWrite1h +
		// A cache write whose lifetime the backend did not report is charged at
		// the dearer of the two rates, following the same fail-expensive rule an
		// unknown model follows. Assuming the cheaper rate would turn an
		// unreported lifetime into an unintended discount.
		u.CacheWriteTokens*r.genericCacheWriteRate()
	return ceilDiv(perMillion, 1_000_000)
}

func (r Rates) genericCacheWriteRate() int64 {
	if r.CacheWrite != 0 {
		return r.CacheWrite
	}
	return max(r.CacheWrite5m, r.CacheWrite1h)
}

// PriceManaged prices normalized usage, refusing negative or unpriced meters.
func (r Rates) PriceManaged(u managed.Usage) (int64, error) {
	if u.Model.Valid() && r.ModelRef() != u.Model {
		return 0, fmt.Errorf("usage model %s/%s does not match rates %s/%s", u.Model.Provider, u.Model.ID, r.ModelRef().Provider, r.ModelRef().ID)
	}
	values := []int64{u.InputFresh, u.InputCached, u.CacheWrite, u.CacheWrite5m, u.CacheWrite1h, u.Output}
	for _, value := range values {
		if value < 0 {
			return 0, fmt.Errorf("negative normalized usage")
		}
	}
	perMillion := u.InputFresh*r.Input + u.InputCached*r.CacheRead + u.CacheWrite*r.genericCacheWriteRate() +
		u.CacheWrite5m*r.CacheWrite5m + u.CacheWrite1h*r.CacheWrite1h + u.Output*r.Output
	for meter, value := range u.Extra {
		rate, ok := r.extraRate(meter)
		if !ok {
			return 0, fmt.Errorf("unpriced meter %q", meter)
		}
		if value < 0 {
			return 0, fmt.Errorf("negative meter %q", meter)
		}
		perMillion += value * rate
	}
	return ceilDiv(perMillion, 1_000_000), nil
}

func (r Rates) extraRate(meter managed.Meter) (int64, bool) {
	for _, entry := range r.Extra {
		if entry.Meter == meter {
			return entry.Rate, true
		}
	}
	return 0, false
}

// Zero reports whether the rates price everything at nothing, which is a card
// entry that would make a session free. Such an entry is not a valid snapshot:
// the gate refuses to start a session it cannot price.
func (r Rates) Zero() bool {
	return !r.Free && r.Input == 0 && r.Output == 0 && r.CacheRead == 0 && r.CacheWrite5m == 0 && r.CacheWrite1h == 0 && r.CacheWrite == 0 && len(r.Extra) == 0
}

// ModelRef is the model a row prices: its provider-qualified ref, or an
// Anthropic model of the row's name for a row written before refs.
func (r Rates) ModelRef() managed.ModelRef {
	if r.Ref.Valid() {
		return r.Ref
	}
	return managed.ModelRef{Provider: "anthropic", ID: strings.TrimSpace(r.Model)}
}

// ErrBadCard reports a price list that cannot be priced from: a row with no
// model, a negative price, one model listed twice, or a row whose
// provider-qualified ref names a different model from the one it is labeled.
var ErrBadCard = errors.New("the rate card has a row that cannot be priced from")

// Validate checks a card an admin wrote before it is stored. Pricing reads every
// row on every session, so a negative price would credit a project for using a
// model, a row with no model can never be matched, and two rows for one model
// would leave which price applies to the order they were typed in. A row whose
// ref names another model is a rename that left the ref behind: the ref is
// what pricing matches, so the row would be charged under its old name.
func (c Card) Validate() error {
	seen := make(map[managed.ModelRef]bool, len(c.Models))
	for i, r := range c.Models {
		model := strings.TrimSpace(r.Model)
		if model == "" && !r.Ref.Valid() {
			return fmt.Errorf("%w: row %d names no model", ErrBadCard, i+1)
		}
		if r.Ref.Valid() && model != "" && r.Ref.ID != model {
			return fmt.Errorf("%w: row %d is labeled %q and priced as %q", ErrBadCard, i+1, model, r.Ref.ID)
		}
		if r.Input < 0 || r.Output < 0 || r.CacheRead < 0 || r.CacheWrite5m < 0 || r.CacheWrite1h < 0 || r.CacheWrite < 0 {
			return fmt.Errorf("%w: %s has a negative price", ErrBadCard, model)
		}
		for _, m := range r.Extra {
			if m.Rate < 0 {
				return fmt.Errorf("%w: %s prices %s below zero", ErrBadCard, model, m.Meter)
			}
		}
		ref := r.ModelRef()
		if seen[ref] {
			return fmt.Errorf("%w: %s is listed twice", ErrBadCard, ref.ID)
		}
		seen[ref] = true
	}
	return nil
}

// Card is the platform's price list: what each model costs. An admin edits it,
// because a vendor's prices change and a constant compiled into the binary
// would make every price change a redeploy and a silent mispricing until then.
type Card struct {
	Models []Rates `json:"models"`
}

// For resolves the rates to charge a model at.
//
// An exact match takes precedence. A dated snapshot of a model the card names
// is charged at that model's rates: a backend listing
// `claude-haiku-4-5-20251001` is offering Claude Haiku 4.5, and billing it as
// an unknown model would charge it the dearest rates on the card, five times
// its real price, on the tier chosen for being cheap. Any other model is
// charged at the dearest rates, class by class: never at zero, which would make
// a vendor's new model id free compute, and never by refusing to start, which
// would make it an outage. The synthesized entry keeps the requested model's
// name, so a snapshot records what was asked for and the amount records what
// it was charged as.
//
// An empty card cannot price anything and returns ErrNoRates. That is
// deliberately fail-closed: tokens that cannot be priced are not free.
func (c Card) For(model string) (Rates, error) {
	return c.ForRef(managed.ModelRef{Provider: "anthropic", ID: strings.TrimSpace(model)})
}

// ForRef is For over a provider-qualified model, and is the form the pricing
// path uses; For is the bare-id convenience that assumes Anthropic.
//
// The fail-expensive fallback uses only entries sharing the model's provider
// and billing variant, so one provider's prices never stand in for another's,
// and a card holding nothing comparable returns ErrNoRates rather than the
// dearest rates of every entry.
func (c Card) ForRef(model managed.ModelRef) (Rates, error) {
	if hit, ok := c.PricedRef(model); ok {
		return hit, nil
	}
	priced := make([]Rates, 0, len(c.Models))
	for _, r := range c.Models {
		if r.Zero() || r.Free || r.ModelRef().Provider != model.Provider || r.ModelRef().BillingVariant != model.BillingVariant {
			continue // a free row prices nothing; it cannot stand in for a model
		}
		priced = append(priced, r)
	}
	if len(priced) == 0 {
		return Rates{}, ErrNoRates
	}
	dearest := Rates{Model: model.ID, Ref: model}
	for _, r := range priced {
		dearest.Input = max(dearest.Input, r.Input)
		dearest.Output = max(dearest.Output, r.Output)
		dearest.CacheRead = max(dearest.CacheRead, r.CacheRead)
		dearest.CacheWrite5m = max(dearest.CacheWrite5m, r.CacheWrite5m)
		dearest.CacheWrite1h = max(dearest.CacheWrite1h, r.CacheWrite1h)
		dearest.CacheWrite = max(dearest.CacheWrite, r.CacheWrite)
		for _, entry := range r.Extra {
			current, ok := dearest.extraRate(entry.Meter)
			if !ok {
				dearest.Extra = append(dearest.Extra, entry)
				continue
			}
			if entry.Rate > current {
				for index := range dearest.Extra {
					if dearest.Extra[index].Meter == entry.Meter {
						dearest.Extra[index].Rate = entry.Rate
					}
				}
			}
		}
	}
	return dearest, nil
}

// Priced resolves a model to rates the card names for it, either an exact
// entry or a dated snapshot of one, and reports whether it found any.
//
// It is For without the fail-expensive fallback. That fallback exists so an
// unrecognized session model is charged the dearest rates rather than nothing.
// A caller that must refuse an unknown model instead of charging it high, such
// as 039's drafting, where the model is an operator's choice and may be a
// cheap local one, uses Priced instead.
func (c Card) Priced(model string) (Rates, bool) {
	return c.PricedRef(managed.ModelRef{Provider: "anthropic", ID: strings.TrimSpace(model)})
}

// PricedRef is Priced over a provider-qualified model: the exact or dated
// snapshot lookup, with no fallback. A ref that does not name a model is not
// priced, and an exact match is preferred over a snapshot regardless of entry
// order.
func (c Card) PricedRef(model managed.ModelRef) (Rates, bool) {
	if !model.Valid() {
		return Rates{}, false
	}
	var snapshot *Rates
	for _, r := range c.Models {
		if r.Zero() {
			continue // a free row prices nothing; it cannot stand in for a model
		}
		ref := r.ModelRef()
		if ref.Provider != model.Provider || ref.BillingVariant != model.BillingVariant {
			continue
		}
		name := strings.TrimSpace(ref.ID)
		if strings.EqualFold(name, model.ID) {
			return r, true
		}
		if snapshot == nil && isSnapshotOf(name, model.ID) {
			hit := r
			hit.Model = model.ID // record what was asked for, priced as what it is
			hit.Ref = model
			snapshot = &hit
		}
	}
	if snapshot != nil {
		return *snapshot, true
	}
	return Rates{}, false
}

// isSnapshotOf reports whether model is a dated snapshot of the card entry
// named alias, for example `claude-haiku-4-5-20251001` of `claude-haiku-4-5`.
//
// The suffix must be a date and nothing else. A plain prefix test would
// misprice silently: a card naming `claude-opus-4` would match
// `claude-opus-4-8`, a different model at a different price, and the
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
// charged rather than dropped. The bias is deliberate and amounts to one
// millionth of a dollar per session; the opposite bias, repeated, would have
// the platform pay the difference.
func ceilDiv(n, d int64) int64 {
	if n <= 0 {
		return 0
	}
	return (n + d - 1) / d
}

// DefaultCard returns the price list this deployment is seeded with: the
// published per-million-token rates for the models the engine's tier
// preference resolves to ("best" is opus, "balanced" is sonnet, "fast" is
// haiku).
//
// Cache rates are derived from the published multipliers rather than typed
// separately, so a card row cannot say a cache read costs more than fresh
// input: a read is a tenth of input, a 5-minute write is 1.25 times it, and an
// hour's write is twice it.
//
// It is a seed, not the authoritative list. Vendor prices change, and a session
// is priced from the platform_settings row, which an admin edits in the
// console; the change applies to sessions starting afterwards. A model the
// card does not name is charged at the dearest rates it holds (Card.For), which
// is why the list stops at the tiers this deployment runs: a model outside them
// is most likely a new variant of the dearest one already listed.
func DefaultCard() Card {
	rates := func(model string, in, out int64) Rates {
		return Rates{
			Model:        model,
			Ref:          managed.ModelRef{Provider: "anthropic", ID: model},
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
		{Model: "memory-model", Ref: managed.ModelRef{Provider: "memory", ID: "memory-model"}, Free: true},
	}}
}
