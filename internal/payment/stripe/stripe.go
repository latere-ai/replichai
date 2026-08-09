// Package stripe is the Stripe implementation of payment.Provider (022b). It is
// the one place the vendor is named; everything upstream depends on the port,
// not on this. EUR is not here: the checkout is always created in USD, and an EU
// customer sees the euro equivalent through the account's Adaptive Pricing
// setting, which is Stripe configuration rather than code.
package stripe

import (
	"context"
	"encoding/json"
	"os"

	stripe "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/checkout/session"
	"github.com/stripe/stripe-go/v82/webhook"

	"github.com/latere-ai/replichai/internal/payment"
)

// Adapter implements payment.Provider over Stripe. An unconfigured adapter (no
// keys) refuses every operation with payment.ErrUnconfigured, so a deployment
// that does not sell credits still boots and runs unmetered.
type Adapter struct {
	webhookSecret string
	configured    bool
}

var _ payment.Provider = (*Adapter)(nil)

// New builds the adapter from the environment: STRIPE_SECRET_KEY and
// STRIPE_WEBHOOK_SECRET (the publishable key is the client's, not read here).
// With either absent it returns a refusing adapter rather than nil, so the
// caller never dereferences a missing provider.
func New() *Adapter {
	secret := os.Getenv("STRIPE_SECRET_KEY")
	whsec := os.Getenv("STRIPE_WEBHOOK_SECRET")
	if secret == "" || whsec == "" {
		return &Adapter{}
	}
	// The app talks to one Stripe account, so the process-global key is the
	// account key. Set once, at construction.
	stripe.Key = secret
	return &Adapter{webhookSecret: whsec, configured: true}
}

// CreateCheckout opens a hosted Checkout Session in USD for the amount the
// person is buying, carrying the email and the pre-computed credited amount in
// metadata so the webhook can post it without recomputing.
func (a *Adapter) CreateCheckout(_ context.Context, p payment.CheckoutParams) (string, error) {
	if !a.configured {
		return "", payment.ErrUnconfigured
	}
	params := &stripe.CheckoutSessionParams{
		Mode:       stripe.String(string(stripe.CheckoutSessionModePayment)),
		SuccessURL: stripe.String(p.SuccessURL),
		CancelURL:  stripe.String(p.CancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{{
			PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
				Currency:   stripe.String(string(stripe.CurrencyUSD)),
				UnitAmount: stripe.Int64(p.AmountMinor),
				ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
					Name: stripe.String("replichai credits"),
				},
			},
			Quantity: stripe.Int64(1),
		}},
	}
	params.AddMetadata("email", p.Email)
	for k, v := range p.Meta {
		params.AddMetadata(k, v)
	}
	sess, err := session.New(params)
	if err != nil {
		return "", err
	}
	return sess.URL, nil
}

// ParseWebhook verifies the delivery's signature and reduces a paid checkout to
// the flat Event the handler acts on. A signature that does not verify is
// ErrBadSignature — the handler posts nothing. Every other event, and a
// completed-but-unpaid session, reduces to a benign Event the handler ignores.
func (a *Adapter) ParseWebhook(payload []byte, sigHeader string) (payment.Event, error) {
	if !a.configured {
		return payment.Event{}, payment.ErrUnconfigured
	}
	// IgnoreAPIVersionMismatch: the signature is what authenticates the delivery,
	// and we read only stable fields (metadata, payment_intent, payment_status),
	// so a webhook endpoint on a different API version than this pinned SDK must
	// not crash crediting. A bad signature still fails closed below.
	event, err := webhook.ConstructEventWithOptions(payload, sigHeader, a.webhookSecret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		return payment.Event{}, payment.ErrBadSignature
	}
	if event.Type == "checkout.session.completed" {
		var s stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &s); err != nil {
			return payment.Event{}, err
		}
		if s.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
			return payment.Event{}, nil // completed but not paid: ignore
		}
		var pi string
		if s.PaymentIntent != nil {
			pi = s.PaymentIntent.ID
		}
		return payment.Event{
			Kind:          payment.EventCheckoutCompleted,
			Email:         s.Metadata["email"],
			PaymentIntent: pi,
			Meta:          s.Metadata,
		}, nil
	}
	// Refund and dispute kinds (022c) pass through by name; everything else is
	// carried as-is for the handler to ignore.
	return payment.Event{Kind: payment.EventKind(event.Type)}, nil
}
