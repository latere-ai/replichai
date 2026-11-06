// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/latere-ai/replichai/internal/access"

	"github.com/latere-ai/replichai/internal/credit"
	"github.com/latere-ai/replichai/internal/platform"

	"latere.ai/x/pay"
	"latere.ai/x/pay/money"
)

// paymentsDeps wires a router over a real ledger, a fake payment provider, and
// the real platform policy, whose seeded spread is 5% + $0.30 and whose floor
// is $5, so these tests exercise the credit path the server runs.
func paymentsDeps(t *testing.T) (Deps, *Router, credit.Store, *pay.MemProvider) {
	t.Helper()
	d := testDeps(allowAll)
	members, ok := d.Members.(credit.Members)
	if !ok {
		t.Fatalf("the test membership store %T cannot answer roles", d.Members)
	}
	ledger := credit.NewMemStore(nil, members)
	prov := &pay.MemProvider{URL: "https://pay.test/session", Secret: "whsec"}
	d.Credits = ledger
	d.Payments = prov
	d.Platform = platform.NewMemStore(nil)
	d.BaseURL = "https://app.test"
	return d, NewRouter(d), ledger, prov
}

func TestStartCheckoutQuotesAndOpensASession(t *testing.T) {
	_, rt, _, prov := paymentsDeps(t)
	w := httptest.NewRecorder()
	// $20.00 gross less the 5% + $0.30 spread credits $18.70.
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/me/credits/checkout", `{"amount_micro":20000000}`, payer.Sub, payer.Email))
	if w.Code != http.StatusOK {
		t.Fatalf("checkout = %d, want 200 (%s)", w.Code, w.Body)
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got.URL != "https://pay.test/session" {
		t.Fatalf("checkout body = %s, %v; want the session URL", w.Body, err)
	}
	if len(prov.Checkouts()) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(prov.Checkouts()))
	}
	c := prov.Checkouts()[0]
	// The provider interface carries the amount in micro-USD. Converting to the
	// processor's minor units is the adapter's job, not this handler's.
	if c.Email != payer.Email || c.Currency != money.USD || c.Amount != 20*money.Dollar {
		t.Fatalf("checkout params = %+v; want the payer, USD, $20", c)
	}
	if c.Meta["credited_micro"] != "18700000" {
		t.Fatalf("credited_micro = %q, want 18700000 (gross − 5%% − $0.30)", c.Meta["credited_micro"])
	}
	if c.SuccessURL != "https://app.test/wallet?checkout=success" {
		t.Fatalf("success url = %q", c.SuccessURL)
	}
}

func TestStartCheckoutRefusesBelowTheFloor(t *testing.T) {
	_, rt, _, prov := paymentsDeps(t)
	w := httptest.NewRecorder()
	// $1.00, below the seeded $5 floor.
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/me/credits/checkout", `{"amount_micro":1000000}`, payer.Sub, payer.Email))
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "below_min") {
		t.Fatalf("below floor = %d %s, want 400 below_min", w.Code, w.Body)
	}
	if len(prov.Checkouts()) != 0 {
		t.Fatalf("a below-floor amount reached the processor: %+v", prov.Checkouts())
	}
}

func TestStartCheckoutUnconfigured(t *testing.T) {
	d, _, _, _ := paymentsDeps(t)
	d.Payments = nil // This deployment sells no credits.
	rt := NewRouter(d)
	w := httptest.NewRecorder()
	rt.Handler().ServeHTTP(w, asPrincipal("POST", "/me/credits/checkout", `{"amount_micro":20000000}`, payer.Sub, payer.Email))
	if w.Code != http.StatusNotImplemented || !strings.Contains(w.Body.String(), "payments_unconfigured") {
		t.Fatalf("unconfigured = %d %s, want 501 payments_unconfigured", w.Code, w.Body)
	}
}

func TestStripeWebhookCreditsOnceAndRefusesABadSignature(t *testing.T) {
	_, rt, ledger, _ := paymentsDeps(t)
	body, _ := json.Marshal(pay.Event{
		Kind: pay.KindPaid, Email: payer.Email, Ref: "pi_1",
		Meta: map[string]string{"credited_micro": "18700000"},
	})
	post := func(sig string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/stripe/webhook", bytes.NewReader(body))
		r.Header.Set(pay.MemHeader, sig)
		rt.Handler().ServeHTTP(w, r)
		return w
	}
	bal := func() int64 {
		n, _ := ledger.Balance(context.Background(), credit.Wallet(payer.Email))
		return n
	}

	// A signature that does not verify posts nothing.
	if w := post("wrong"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad signature = %d, want 400", w.Code)
	}
	if bal() != 0 {
		t.Fatalf("balance after bad signature = %d, want 0", bal())
	}

	// A verified delivery credits the metadata amount; a retried delivery of the
	// same payment_intent does not double it.
	if w := post("whsec"); w.Code != http.StatusOK {
		t.Fatalf("first delivery = %d (%s)", w.Code, w.Body)
	}
	if w := post("whsec"); w.Code != http.StatusOK {
		t.Fatalf("retried delivery = %d", w.Code)
	}
	if bal() != 18_700_000 {
		t.Fatalf("balance = %d, want 18700000 credited exactly once", bal())
	}
}

// TestStripeWebhookRefundReversesTheCreditedAmount checks that a refund
// reverses the exact micro-USD the purchase credited rather than a fresh
// conversion, and is keyed on its own id so it deduplicates independently. The
// wallet may go negative, and a retried refund does not reverse the amount
// twice.
func TestStripeWebhookRefundReversesTheCreditedAmount(t *testing.T) {
	_, rt, ledger, _ := paymentsDeps(t)
	deliver := func(v pay.Event) int {
		body, _ := json.Marshal(v)
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/stripe/webhook", bytes.NewReader(body))
		r.Header.Set(pay.MemHeader, "whsec")
		rt.Handler().ServeHTTP(w, r)
		return w.Code
	}
	bal := func() int64 {
		n, _ := ledger.Balance(context.Background(), credit.Wallet(payer.Email))
		return n
	}

	// A purchase credits $18.70.
	if code := deliver(pay.Event{Kind: pay.KindPaid, Email: payer.Email, Ref: "pi_1", Meta: map[string]string{"credited_micro": "18700000"}}); code != http.StatusOK {
		t.Fatalf("purchase = %d, want 200", code)
	}
	// The refund names the purchase by its payment_intent and carries its own id.
	refund := pay.Event{Kind: pay.KindRefunded, Ref: "pi_1", ReversalRef: "re_1"}
	if code := deliver(refund); code != http.StatusOK {
		t.Fatalf("refund = %d, want 200", code)
	}
	if code := deliver(refund); code != http.StatusOK { // A retried delivery.
		t.Fatalf("retried refund = %d, want 200", code)
	}
	if bal() != 0 {
		t.Fatalf("balance after one refund = %d, want 0 (the credit reversed exactly once)", bal())
	}

	// A refund for a purchase this ledger never recorded is acknowledged and
	// dropped. It is neither retried nor treated as an error.
	if code := deliver(pay.Event{Kind: pay.KindRefunded, Ref: "pi_unknown", ReversalRef: "re_x"}); code != http.StatusOK {
		t.Fatalf("refund of unknown purchase = %d, want 200", code)
	}
}

// TestStripeWebhookRefundEmailsOnTheCrossingOnlyOnce checks that the freeze
// email is sent once, on the reversal that first takes the wallet negative,
// which is a chargeback of credit already spent, and not again on a further
// refund of an account that is already frozen.
func TestStripeWebhookRefundEmailsOnTheCrossingOnlyOnce(t *testing.T) {
	ctx := context.Background()
	d, _, ledger, _ := paymentsDeps(t)
	nf := &fakeNotifier{}
	d.Notifier = nf
	rt := NewRouter(d)
	deliver := func(v pay.Event) {
		body, _ := json.Marshal(v)
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/stripe/webhook", bytes.NewReader(body))
		r.Header.Set(pay.MemHeader, "whsec")
		rt.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("webhook %s = %d (%s)", v.Kind, w.Code, w.Body)
		}
	}
	purchase := func(pi string, credited int64) {
		deliver(pay.Event{Kind: pay.KindPaid, Email: payer.Email, Ref: pi, Meta: map[string]string{"credited_micro": itoa(credited)}})
	}

	// $5 is bought and allocated to a project the payer leads, so the wallet
	// is back at 0 and the refund of that purchase takes it negative. The
	// membership is seeded the way the create route seeds its owner, so the
	// ledger's membership rule admits the allocation.
	purchase("pi_1", 5*credit.Dollar)
	lead := access.Principal{Sub: payer.Sub, Email: payer.Email}
	if err := d.Members.(*access.MemMembership).SeedLead(ctx, "proj-1", lead); err != nil {
		t.Fatalf("SeedLead: %v", err)
	}
	if err := ledger.Allocate(ctx, lead, "proj-1", "P", 5*credit.Dollar); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	deliver(pay.Event{Kind: pay.KindRefunded, Ref: "pi_1", ReversalRef: "re_1"})
	if got := nf.frozenSends(); len(got) != 1 || got[0] != payer.Email {
		t.Fatalf("after the crossing, freeze emails = %v; want one to the payer", got)
	}

	// A second, unspent purchase and its refund take the wallet further
	// negative but do not send another email, because the account was already
	// frozen.
	purchase("pi_2", 2*credit.Dollar)
	deliver(pay.Event{Kind: pay.KindRefunded, Ref: "pi_2", ReversalRef: "re_2"})
	if got := nf.frozenSends(); len(got) != 1 {
		t.Fatalf("a further refund of an already-frozen account emailed again: %v", got)
	}
}
