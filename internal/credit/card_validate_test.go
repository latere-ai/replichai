// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package credit

import (
	"errors"
	"testing"

	"github.com/latere-ai/replichai/internal/managed"
)

// A card an admin edits by hand is checked before it is stored. Pricing reads
// every row of it on every session: a negative price would credit a project
// for using a model, a row with no model can never be matched, and two rows
// for one model leave which price applies to the order they were typed in.
func TestCardValidate(t *testing.T) {
	good := Rates{Model: "claude-opus-5-5", Input: 5 * Dollar, Output: 25 * Dollar, CacheRead: Dollar / 2, CacheWrite5m: 6 * Dollar, CacheWrite1h: 10 * Dollar}
	if err := DefaultCard().Validate(); err != nil {
		t.Fatalf("the compiled default card is refused: %v", err)
	}
	if err := (Card{Models: []Rates{good}}).Validate(); err != nil {
		t.Fatalf("a card of one priced model is refused: %v", err)
	}
	// A row the store receives with a provider-qualified ref whose id agrees
	// with the model is the same row.
	withRef := good
	withRef.Ref = managed.ModelRef{Provider: "anthropic", ID: good.Model}
	if err := (Card{Models: []Rates{withRef}}).Validate(); err != nil {
		t.Fatalf("a row whose ref names its model is refused: %v", err)
	}

	bad := map[string]Card{
		"a row with no model": {Models: []Rates{{Input: Dollar}}},
		"a negative input":    {Models: []Rates{{Model: "m", Input: -1, Output: Dollar}}},
		"a negative output":   {Models: []Rates{{Model: "m", Input: Dollar, Output: -1}}},
		"a negative cache":    {Models: []Rates{{Model: "m", Input: Dollar, CacheWrite1h: -1}}},
		"a negative meter":    {Models: []Rates{{Model: "m", Input: Dollar, Extra: []MeterRate{{Meter: "container_second", Rate: -1}}}}},
		"one model twice":     {Models: []Rates{good, good}},
		// A row renamed by hand and left with its old ref would be priced
		// under the old name, since the ref is what pricing matches.
		"a ref naming another model": {Models: []Rates{{Model: "claude-opus-5-5", Ref: managed.ModelRef{Provider: "anthropic", ID: "claude-opus-5"}, Input: Dollar}}},
	}
	for name, card := range bad {
		if err := card.Validate(); !errors.Is(err, ErrBadCard) {
			t.Errorf("%s: Validate = %v, want ErrBadCard", name, err)
		}
	}
}
