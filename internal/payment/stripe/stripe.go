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
		// VAT is deferred to its own spec (024). Keep the charge equal to exactly
		// what the app quoted before the redirect: turn off automatic tax, and
		// turn off Managed Payments (default-on for new accounts) — it otherwise
		// requires a product tax code and adds tax on top of the total. When 024
		// lands, this reverses: automatic_tax on, a real tax code, invoices.
		AutomaticTax: &stripe.CheckoutSessionAutomaticTaxParams{Enabled: stripe.Bool(false)},
	}
	// No typed field for Managed Payments in this SDK version; the exact param is
	// the one Stripe's own error names.
	params.AddExtra("managed_payments[enabled]", "false")
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
	switch event.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		var s stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &s); err != nil {
			return payment.Event{}, err
		}
		// A synchronous card pays immediately, so `completed` is `paid`. An async
		// method — SEPA Direct Debit, iDEAL, Bancontact, the ones EU customers
		// reach for — leaves `completed` unpaid and confirms later with
		// `async_payment_succeeded`. We credit only a *paid* session, so the two
		// events for one async purchase still credit exactly once: the ledger
		// dedupes on the payment_intent, and the unpaid `completed` is ignored.
		if s.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
			return payment.Event{}, nil
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
	case "charge.refunded":
		// Minimal fields, parsed by hand rather than through the SDK's expandable
		// types, so a version bump cannot change the shape underneath us. The
		// payment_intent finds the original purchase; the refund id is this
		// reversal's own idempotency key.
		var c struct {
			ID            string `json:"id"`
			PaymentIntent string `json:"payment_intent"`
			Refunds       struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			} `json:"refunds"`
		}
		if err := json.Unmarshal(event.Data.Raw, &c); err != nil {
			return payment.Event{}, err
		}
		ref := c.ID
		if n := len(c.Refunds.Data); n > 0 {
			ref = c.Refunds.Data[n-1].ID
		}
		return payment.Event{Kind: payment.EventChargeRefunded, PaymentIntent: c.PaymentIntent, RefundID: ref}, nil
	case "charge.dispute.created":
		var d struct {
			ID            string `json:"id"`
			PaymentIntent string `json:"payment_intent"`
		}
		if err := json.Unmarshal(event.Data.Raw, &d); err != nil {
			return payment.Event{}, err
		}
		return payment.Event{Kind: payment.EventDisputeCreated, PaymentIntent: d.PaymentIntent, RefundID: d.ID}, nil
	default:
		// Everything else is carried as-is for the handler to ignore.
		return payment.Event{Kind: payment.EventKind(event.Type)}, nil
	}
}
