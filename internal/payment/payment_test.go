package payment

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// The port is satisfied by the memory fake, so a test that depends on a Provider
// needs no Stripe. Guards the interface too: MemProvider must remain a Provider.
var _ Provider = (*MemProvider)(nil)

func TestMemProviderCreateCheckoutRecordsAndReturnsAURL(t *testing.T) {
	ctx := context.Background()

	// With no URL set, a default keeps a test that does not care about the URL
	// from having to configure one.
	def := &MemProvider{}
	if url, err := def.CreateCheckout(ctx, CheckoutParams{Email: "a@b.c", AmountMinor: 500, Currency: USD}); err != nil || url == "" {
		t.Fatalf("default checkout = %q, %v; want a non-empty URL", url, err)
	}

	m := &MemProvider{URL: "https://pay.test/x"}
	p := CheckoutParams{Email: "buyer@latere.ai", AmountMinor: 2000, Currency: EUR, Meta: map[string]string{"spread_bps": "290"}}
	url, err := m.CreateCheckout(ctx, p)
	if err != nil || url != "https://pay.test/x" {
		t.Fatalf("checkout = %q, %v; want the canned URL", url, err)
	}
	if len(m.Calls) != 1 || m.Calls[0].Currency != EUR || m.Calls[0].Meta["spread_bps"] != "290" {
		t.Fatalf("recorded calls = %+v; want the params passed", m.Calls)
	}
}

func TestMemProviderParseWebhookVerifiesAndDecodes(t *testing.T) {
	m := &MemProvider{Secret: "whsec_test"}
	body, _ := json.Marshal(Event{Kind: EventCheckoutCompleted, Email: "buyer@latere.ai", PaymentIntent: "pi_1", GrossUSDMicro: 21_000_000})

	// A verified delivery reduces to the flat Event the handler acts on.
	e, err := m.ParseWebhook(body, "whsec_test")
	if err != nil {
		t.Fatalf("ParseWebhook: %v", err)
	}
	if e.Kind != EventCheckoutCompleted || e.PaymentIntent != "pi_1" || e.GrossUSDMicro != 21_000_000 {
		t.Fatalf("decoded %+v; want the encoded event", e)
	}

	// A wrong signature authenticates nothing.
	if _, err := m.ParseWebhook(body, "wrong"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("bad signature: want ErrBadSignature, got %v", err)
	}

	// Malformed JSON is an error, not a zero Event silently accepted.
	if _, err := m.ParseWebhook([]byte("{not json"), "whsec_test"); err == nil {
		t.Fatalf("malformed payload: want an error, got nil")
	}

	// An empty Secret accepts any signature — what an offline test that is not
	// about authentication wants.
	open := &MemProvider{}
	if _, err := open.ParseWebhook(body, ""); err != nil {
		t.Fatalf("open provider: %v", err)
	}
}
