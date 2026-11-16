// SPDX-FileCopyrightText: 2026 Latere AI
// SPDX-License-Identifier: AGPL-3.0-or-later

package httpapi

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/access"
	"github.com/latere-ai/replichai/internal/credit"

	"latere.ai/x/pay"
)

// TestStripeWebhookRefundAndDisputeBeforeTheirPurchaseAreRetried checks that a
// refund or dispute that arrives before its purchase is recorded is answered
// with a non-2xx status, and that the same delivery succeeds and reverses the
// credit once the purchase has landed.
func TestStripeWebhookRefundAndDisputeBeforeTheirPurchaseAreRetried(t *testing.T) {
	for _, kind := range []pay.Kind{pay.KindRefunded, pay.KindDisputed} {
		t.Run(string(kind), func(t *testing.T) {
			_, rt, ledger, _ := paymentsDeps(t)
			reversal := refundDelivery(t, pay.Event{Kind: kind, Ref: "pi_late", ReversalRef: "rv_1"}, 2000, 2000)
			if code := rt.deliverWebhook(reversal); code < 400 {
				t.Fatalf("%s before its purchase = %d, want a status Stripe retries", kind, code)
			}
			paid, err := json.Marshal(pay.Event{Kind: pay.KindPaid, Email: payer.Email, Ref: "pi_late", Meta: map[string]string{"credited_micro": "18700000"}})
			if err != nil {
				t.Fatal(err)
			}
			if code := rt.deliverWebhook(paid); code != http.StatusOK {
				t.Fatalf("purchase = %d, want 200", code)
			}
			if code := rt.deliverWebhook(reversal); code != http.StatusOK {
				t.Fatalf("retried %s = %d, want 200", kind, code)
			}
			if bal, err := ledger.Balance(context.Background(), credit.Wallet(payer.Email)); err != nil || bal != 0 {
				t.Fatalf("balance = %d, %v; want 0 after the retried reversal", bal, err)
			}
		})
	}
}

// TestStripeWebhookWithoutPaymentsIsUnavailable checks that a deployment with
// payments unconfigured answers a payment webhook with 503, not 200, so the
// processor does not treat the event as delivered.
func TestStripeWebhookWithoutPaymentsIsUnavailable(t *testing.T) {
	d, _, _, _ := paymentsDeps(t)
	d.Payments = nil
	rt := NewRouter(d)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/stripe/webhook", strings.NewReader("{}"))
	rt.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("webhook without payments = %d, want 503", w.Code)
	}
}

// TestStripeWebhookConcurrentRefundsEmailTheFreezeOnce checks that two refunds
// of different purchases of one wallet, delivered at the same time, send the
// account-frozen mail once: only one of them moves the wallet across zero.
func TestStripeWebhookConcurrentRefundsEmailTheFreezeOnce(t *testing.T) {
	for round := range 20 {
		ctx := context.Background()
		d, _, ledger, _ := paymentsDeps(t)
		nf := &fakeNotifier{}
		d.Notifier = nf
		rt := NewRouter(d)
		for _, pi := range []string{"pi_1", "pi_2"} {
			body, err := json.Marshal(pay.Event{Kind: pay.KindPaid, Email: payer.Email, Ref: pi, Meta: map[string]string{"credited_micro": itoa(5 * credit.Dollar)}})
			if err != nil {
				t.Fatal(err)
			}
			if code := rt.deliverWebhook(body); code != http.StatusOK {
				t.Fatalf("purchase %s = %d", pi, code)
			}
		}
		// $9 of the $10 goes to a project, leaving $1: either full refund
		// alone leaves the wallet at -$4, and both leave it at -$9.
		lead := access.Principal{Sub: payer.Sub, Email: payer.Email}
		if err := d.Members.(*access.MemMembership).SeedLead(ctx, "proj-1", lead); err != nil {
			t.Fatalf("SeedLead: %v", err)
		}
		if err := ledger.Allocate(ctx, lead, "proj-1", "P", 9*credit.Dollar); err != nil {
			t.Fatalf("Allocate: %v", err)
		}
		var wg sync.WaitGroup
		codes := make([]int, 2)
		for i, pi := range []string{"pi_1", "pi_2"} {
			wg.Go(func() {
				codes[i] = rt.deliverWebhook(refundDelivery(t, pay.Event{Kind: pay.KindRefunded, Ref: pi, ReversalRef: "re_" + pi}, 500, 500))
			})
		}
		wg.Wait()
		for _, c := range codes {
			if c != http.StatusOK {
				t.Fatalf("round %d: refund = %d, want 200", round, c)
			}
		}
		if got := nf.frozenSends(); len(got) != 1 {
			t.Fatalf("round %d: freeze mails = %v, want exactly one", round, got)
		}
	}
}

// TestStripeWebhookAWonDisputeGivesTheCreditBackOnce checks that a won dispute
// restores what the dispute took, and that a second delivery of the outcome, as
// Stripe sends for one win, credits nothing more.
func TestStripeWebhookAWonDisputeGivesTheCreditBackOnce(t *testing.T) {
	_, rt, ledger, _ := paymentsDeps(t)
	paid, err := json.Marshal(pay.Event{Kind: pay.KindPaid, Email: payer.Email, Ref: "pi_w", Meta: map[string]string{"credited_micro": "18700000"}})
	if err != nil {
		t.Fatal(err)
	}
	if code := rt.deliverWebhook(paid); code != http.StatusOK {
		t.Fatalf("purchase = %d", code)
	}
	if code := rt.deliverWebhook(refundDelivery(t, pay.Event{Kind: pay.KindDisputed, Ref: "pi_w", ReversalRef: "dp_w"}, 0, 0)); code != http.StatusOK {
		t.Fatalf("dispute = %d", code)
	}
	bal := func() int64 {
		n, _ := ledger.Balance(context.Background(), credit.Wallet(payer.Email))
		return n
	}
	if bal() != 0 {
		t.Fatalf("balance after the dispute = %d, want 0", bal())
	}
	won, err := json.Marshal(pay.Event{Kind: pay.KindDisputeWon, Ref: "pi_w", ReversalRef: "dp_w"})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if code := rt.deliverWebhook(won); code != http.StatusOK {
			t.Fatalf("won dispute = %d, want 200", code)
		}
	}
	if bal() != 18_700_000 {
		t.Fatalf("balance after a won dispute delivered twice = %d, want the credit back once", bal())
	}
	unknown, err := json.Marshal(pay.Event{Kind: pay.KindDisputeWon, Ref: "pi_none", ReversalRef: "dp_none"})
	if err != nil {
		t.Fatal(err)
	}
	if code := rt.deliverWebhook(unknown); code != http.StatusOK {
		t.Fatalf("won dispute of an unrecorded purchase = %d, want 200: a dispute is decided weeks after its purchase", code)
	}
}

// delivery is a webhook body for ev whose processor object is obj, as Stripe
// sends it under data.object.
func delivery(t *testing.T, ev pay.Event, obj map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	m["data"] = map[string]any{"object": obj}
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestStripeWebhookRefundsWithoutARefundListEachReverse checks that two partial
// refunds of one charge each take back their share when the charge.refunded
// payload names no refund, so both carry the charge's own id, and that a
// redelivery of the first takes back nothing more.
func TestStripeWebhookRefundsWithoutARefundListEachReverse(t *testing.T) {
	_, rt, ledger, _ := paymentsDeps(t)
	bal := func() int64 {
		n, _ := ledger.Balance(context.Background(), credit.Wallet(payer.Email))
		return n
	}
	paid, err := json.Marshal(pay.Event{Kind: pay.KindPaid, Email: payer.Email, Ref: "pi_nl", Meta: map[string]string{"credited_micro": "20000000"}})
	if err != nil {
		t.Fatal(err)
	}
	if code := rt.deliverWebhook(paid); code != http.StatusOK {
		t.Fatalf("purchase = %d", code)
	}
	refund := func(refunded int64) []byte {
		return refundDelivery(t, pay.Event{Kind: pay.KindRefunded, Ref: "pi_nl", ReversalRef: "ch_nl"}, refunded, 2000)
	}
	first := refund(500)
	for _, body := range [][]byte{first, refund(1000), first} {
		if code := rt.deliverWebhook(body); code != http.StatusOK {
			t.Fatalf("refund = %d, want 200", code)
		}
	}
	if got := bal(); got != 10_000_000 {
		t.Fatalf("after refunds of 25%% and 50%% in all, with the first delivered twice, the balance is %d, want 10000000", got)
	}
}

// TestStripeWebhookAnInquiryTakesNothing checks that a dispute the card
// network opened as an inquiry, which withdraws no money, leaves the credit and
// does not freeze the account, and that a chargeback on the same purchase still
// takes back what is left.
func TestStripeWebhookAnInquiryTakesNothing(t *testing.T) {
	_, rt, ledger, _ := paymentsDeps(t)
	bal := func() int64 {
		n, _ := ledger.Balance(context.Background(), credit.Wallet(payer.Email))
		return n
	}
	paid, err := json.Marshal(pay.Event{Kind: pay.KindPaid, Email: payer.Email, Ref: "pi_iq", Meta: map[string]string{"credited_micro": "18700000"}})
	if err != nil {
		t.Fatal(err)
	}
	if code := rt.deliverWebhook(paid); code != http.StatusOK {
		t.Fatalf("purchase = %d", code)
	}
	for _, status := range []string{"warning_needs_response", "warning_under_review"} {
		inquiry := delivery(t, pay.Event{Kind: pay.KindDisputed, Ref: "pi_iq", ReversalRef: "dp_iq"}, map[string]any{"status": status})
		if code := rt.deliverWebhook(inquiry); code != http.StatusOK {
			t.Fatalf("inquiry %s = %d, want 200", status, code)
		}
		if got := bal(); got != 18_700_000 {
			t.Fatalf("after an inquiry (%s) the balance is %d, want the credit untouched", status, got)
		}
	}
	chargeback := delivery(t, pay.Event{Kind: pay.KindDisputed, Ref: "pi_iq", ReversalRef: "dp_cb"}, map[string]any{"status": "needs_response"})
	if code := rt.deliverWebhook(chargeback); code != http.StatusOK {
		t.Fatalf("chargeback = %d, want 200", code)
	}
	if got := bal(); got != 0 {
		t.Fatalf("after a chargeback the balance is %d, want 0", got)
	}
}

// TestStripeWebhookARefundOfAnOldUnrecordedChargeIsAcknowledged checks that a
// refund whose purchase is not recorded is retried only while the purchase's
// own delivery could still arrive: a charge made longer ago than the processor
// retries for is acknowledged.
func TestStripeWebhookARefundOfAnOldUnrecordedChargeIsAcknowledged(t *testing.T) {
	_, rt, _, _ := paymentsDeps(t)
	for _, c := range []struct {
		name    string
		created time.Time
		want    int
	}{
		{"an hour ago", time.Now().Add(-time.Hour), http.StatusInternalServerError},
		{"four days ago", time.Now().Add(-4 * 24 * time.Hour), http.StatusOK},
	} {
		body := delivery(t, pay.Event{Kind: pay.KindRefunded, Ref: "pi_gone", ReversalRef: "re_gone"},
			map[string]any{"amount": 2000, "amount_refunded": 2000, "created": c.created.Unix()})
		if code := rt.deliverWebhook(body); code != c.want {
			t.Errorf("refund of an unrecorded charge made %s = %d, want %d", c.name, code, c.want)
		}
	}
}
