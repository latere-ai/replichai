package credit

import (
	"context"
	"errors"
	"testing"
)

// stubResolver stands in for a backend's model list, resolved the way the
// adapter resolves it: a preference maps to whatever id that backend offers.
type stubResolver map[string]string

func (s stubResolver) ResolveModel(_ context.Context, pref string) (string, error) {
	id, ok := s[pref]
	if !ok {
		return "", errors.New("no model for " + pref)
	}
	return id, nil
}

// The finding this check exists for: a backend that lists a dated id where the
// card cannot price on its own terms. The session is still priced — never free
// — but at the dearest rates on the card, and an operator should learn that
// before the first invoice rather than after. A *dated snapshot* of a model the
// card names is not that case: it is that model, and is priced as it.
func TestCoversFlagsAModelTheCardDoesNotName(t *testing.T) {
	card := DefaultCard()
	cov := card.Covers(context.Background(), stubResolver{
		"best":     "claude-something-6",
		"balanced": "claude-sonnet-5",
		"fast":     "claude-haiku-4-5-20251001",
	})

	byPref := map[string]Coverage{}
	for _, c := range cov {
		byPref[c.Preference] = c
	}
	if len(cov) != len(Preferences) {
		t.Fatalf("checked %d preferences, want %d", len(cov), len(Preferences))
	}

	best := byPref["best"]
	if best.Named {
		t.Error("a model the card cannot price was reported as priced on its own terms")
	}
	if best.Rates.Input != 5*Dollar {
		t.Errorf("the unnamed model was priced at %d, want the dearest input rate %d", best.Rates.Input, 5*Dollar)
	}
	// "fast" resolved to a *dated snapshot* of a model the card names, which is
	// that model and is priced as it — the production finding that made the
	// snapshot rule exist. It must not read as a warning.
	for _, pref := range []string{"balanced", "fast"} {
		if !byPref[pref].Named {
			t.Errorf("%s resolved to %q and was reported unpriceable by a card that covers it", pref, byPref[pref].Model)
		}
	}
	if byPref["fast"].Rates.Input != 1*Dollar {
		t.Errorf("the dated haiku was priced at %d, want haiku's own %d", byPref["fast"].Rates.Input, 1*Dollar)
	}
	if un := Unnamed(cov); len(un) != 1 || un[0].Preference != "best" {
		t.Errorf("Unnamed = %+v, want just best", un)
	}
}

// The load-bearing subtlety: Card.For synthesises a fallback entry carrying the
// *requested* model's name, so an exact match and a fallback are identical by
// name. A check comparing names would call every model named, and the whole
// report would be silently useless.
func TestCoverageDoesNotTrustTheSynthesisedName(t *testing.T) {
	card := DefaultCard()
	rates, err := card.For("claude-something-6")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	if rates.Model != "claude-something-6" {
		t.Fatalf("the fallback did not carry the requested name: %q", rates.Model)
	}
	if card.names("claude-something-6") {
		t.Error("names() believed the synthesised entry")
	}
	if !card.names("claude-opus-5-20260901") {
		t.Error("names() missed a dated snapshot of a model the card carries")
	}
	if !card.names("claude-opus-5") {
		t.Error("names() missed a model the card really carries")
	}
	// A zero row prices nothing, so it cannot count as naming a model either —
	// otherwise a placeholder entry would report a session as properly priced
	// while charging it the dearest rates.
	placeholder := Card{Models: []Rates{{Model: "claude-opus-5"}, DefaultCard().Models[0]}}
	if placeholder.names("claude-opus-5") != true {
		t.Error("a card with both a zero row and a real one for the same model should count the real one")
	}
	onlyZero := Card{Models: []Rates{{Model: "ghost"}, DefaultCard().Models[0]}}
	if onlyZero.names("ghost") {
		t.Error("a zero row was counted as naming a model; it prices nothing")
	}
}

// A backend that will not answer is reported per preference rather than
// failing the check: the question is about the card, and a resolution failure
// is a different problem that must not read as a pricing finding.
func TestCoversReportsAResolutionFailureWithoutClaimingAFinding(t *testing.T) {
	cov := DefaultCard().Covers(context.Background(), stubResolver{})
	for _, c := range cov {
		if c.Err == "" {
			t.Errorf("%s reported no error against a backend that answered nothing", c.Preference)
		}
		if c.Named {
			t.Errorf("%s claimed a card match it could not have checked", c.Preference)
		}
	}
	if un := Unnamed(cov); len(un) != 0 {
		t.Errorf("Unnamed = %+v; a resolution failure is not a pricing finding", un)
	}
}
