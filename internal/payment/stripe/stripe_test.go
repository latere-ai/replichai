package stripe

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/latere-ai/replichai/internal/payment"
)

// sign builds a Stripe-style signature header for payload, so ParseWebhook can
// be exercised without a live Stripe: `t=<ts>,v1=<hmac-sha256(ts.payload)>`.
func sign(payload []byte, secret string, ts int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	fmt.Fprintf(mac, "%d.%s", ts, payload)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func TestParseWebhookVerifiesAndReducesAPaidCheckout(t *testing.T) {
	a := &Adapter{webhookSecret: "whsec_test", configured: true}
	payload := []byte(`{"type":"checkout.session.completed","data":{"object":{` +
		`"payment_status":"paid","payment_intent":"pi_123",` +
		`"metadata":{"email":"buyer@latere.ai","credited_micro":"18700000"}}}}`)

	// A correctly-signed paid checkout reduces to the flat Event the handler acts on.
	e, err := a.ParseWebhook(payload, sign(payload, "whsec_test", time.Now().Unix()))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if e.Kind != payment.EventCheckoutCompleted || e.Email != "buyer@latere.ai" ||
		e.PaymentIntent != "pi_123" || e.Meta["credited_micro"] != "18700000" {
		t.Fatalf("reduced event = %+v; want the paid checkout's fields", e)
	}

	// A signature under the wrong secret authenticates nothing.
	if _, err := a.ParseWebhook(payload, sign(payload, "wrong", time.Now().Unix())); !errors.Is(err, payment.ErrBadSignature) {
		t.Fatalf("bad signature: want ErrBadSignature, got %v", err)
	}
}

// An async EU payment method (SEPA/iDEAL/Bancontact) leaves `completed` unpaid
// and confirms later with `async_payment_succeeded`; the paid async event must
// credit, so those top-ups are not silently lost.
func TestParseWebhookCreditsAnAsyncPayment(t *testing.T) {
	a := &Adapter{webhookSecret: "whsec_test", configured: true}
	async := []byte(`{"type":"checkout.session.async_payment_succeeded","data":{"object":{` +
		`"payment_status":"paid","payment_intent":"pi_async",` +
		`"metadata":{"email":"eu@latere.ai","credited_micro":"18700000"}}}}`)
	e, err := a.ParseWebhook(async, sign(async, "whsec_test", time.Now().Unix()))
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if e.Kind != payment.EventCheckoutCompleted || e.PaymentIntent != "pi_async" || e.Meta["credited_micro"] != "18700000" {
		t.Fatalf("async payment reduced to %+v; want a crediting event", e)
	}
}

func TestParseWebhookIgnoresAnUnpaidOrOtherEvent(t *testing.T) {
	a := &Adapter{webhookSecret: "whsec_test", configured: true}

	// Completed but not paid — an async method still pending: a benign Event the
	// handler will not credit until async_payment_succeeded arrives.
	unpaid := []byte(`{"type":"checkout.session.completed","data":{"object":{"payment_status":"unpaid","metadata":{"email":"x@y.z"}}}}`)
	if e, err := a.ParseWebhook(unpaid, sign(unpaid, "whsec_test", time.Now().Unix())); err != nil || e.Kind == payment.EventCheckoutCompleted {
		t.Fatalf("unpaid session reduced to %+v, %v; want a non-crediting event", e, err)
	}

	// An unrelated event passes through by name for a later handler, not credited here.
	other := []byte(`{"type":"payment_intent.created","data":{"object":{}}}`)
	if e, err := a.ParseWebhook(other, sign(other, "whsec_test", time.Now().Unix())); err != nil || e.Kind == payment.EventCheckoutCompleted {
		t.Fatalf("other event reduced to %+v, %v; want a non-crediting event", e, err)
	}
}

// A refund and a dispute reduce to events carrying the original purchase's
// payment_intent (to find what to reverse) and their own id (the reversal's
// idempotency key), so 022c can claw the credit back exactly once.
func TestParseWebhookReducesRefundsAndDisputes(t *testing.T) {
	a := &Adapter{webhookSecret: "whsec_test", configured: true}

	refund := []byte(`{"type":"charge.refunded","data":{"object":{` +
		`"id":"ch_1","payment_intent":"pi_1","refunds":{"data":[{"id":"re_1"}]}}}}`)
	if e, err := a.ParseWebhook(refund, sign(refund, "whsec_test", time.Now().Unix())); err != nil ||
		e.Kind != payment.EventChargeRefunded || e.PaymentIntent != "pi_1" || e.RefundID != "re_1" {
		t.Fatalf("refund reduced to %+v, %v; want the pi and refund id", e, err)
	}

	dispute := []byte(`{"type":"charge.dispute.created","data":{"object":{"id":"dp_1","payment_intent":"pi_1"}}}`)
	if e, err := a.ParseWebhook(dispute, sign(dispute, "whsec_test", time.Now().Unix())); err != nil ||
		e.Kind != payment.EventDisputeCreated || e.PaymentIntent != "pi_1" || e.RefundID != "dp_1" {
		t.Fatalf("dispute reduced to %+v, %v; want the pi and dispute id", e, err)
	}
}

func TestUnconfiguredAdapterRefuses(t *testing.T) {
	a := &Adapter{} // no keys
	if _, err := a.CreateCheckout(context.Background(), payment.CheckoutParams{AmountMinor: 2000}); !errors.Is(err, payment.ErrUnconfigured) {
		t.Fatalf("CreateCheckout unconfigured: want ErrUnconfigured, got %v", err)
	}
	if _, err := a.ParseWebhook([]byte(`{}`), "sig"); !errors.Is(err, payment.ErrUnconfigured) {
		t.Fatalf("ParseWebhook unconfigured: want ErrUnconfigured, got %v", err)
	}
}
