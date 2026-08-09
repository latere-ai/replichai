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

func TestParseWebhookIgnoresAnUnpaidOrOtherEvent(t *testing.T) {
	a := &Adapter{webhookSecret: "whsec_test", configured: true}

	// Completed but not paid: a benign Event the handler will not credit.
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

func TestUnconfiguredAdapterRefuses(t *testing.T) {
	a := &Adapter{} // no keys
	if _, err := a.CreateCheckout(context.Background(), payment.CheckoutParams{AmountMinor: 2000}); !errors.Is(err, payment.ErrUnconfigured) {
		t.Fatalf("CreateCheckout unconfigured: want ErrUnconfigured, got %v", err)
	}
	if _, err := a.ParseWebhook([]byte(`{}`), "sig"); !errors.Is(err, payment.ErrUnconfigured) {
		t.Fatalf("ParseWebhook unconfigured: want ErrUnconfigured, got %v", err)
	}
}
