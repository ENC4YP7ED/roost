package billing

import (
	"net/http"
)

// CheckoutRequest is what a provider needs to open a hosted payment page.
type CheckoutRequest struct {
	OrderUUID   string
	Description string
	AmountCents int64
	Currency    string
	Recurring   bool   // month/year → a subscription, else a one-off payment
	Interval    string // "month" | "year" when Recurring
	Email       string
	SuccessURL  string
	CancelURL   string
}

// CheckoutResult is what the browser needs to complete payment.
type CheckoutResult struct {
	RedirectURL string // hosted checkout page the customer is sent to
	ProviderRef string // session/order id we store on the order for reconciliation
}

// EventType normalises the provider-specific webhook events we act on.
type EventType string

const (
	EventPaymentSucceeded EventType = "payment_succeeded"
	EventPaymentFailed    EventType = "payment_failed"
	EventSubscriptionPaid EventType = "subscription_paid"
	EventSubscriptionEnd  EventType = "subscription_canceled"
	EventRefunded         EventType = "refunded"
	EventIgnored          EventType = "ignored"
)

// WebhookEvent is the normalised result of verifying and parsing a callback.
type WebhookEvent struct {
	Type EventType
	// OrderRef / SubRef identify what the event is about at the provider; we
	// look the order/subscription up by (provider, ref).
	OrderRef       string
	SubscriptionID string
	// PeriodEnd is set for subscription renewals (RFC3339).
	PeriodEnd string
}

// Provider is a hosted-checkout payment integration.
type Provider interface {
	Name() string
	CreateCheckout(req CheckoutRequest) (CheckoutResult, error)
	// VerifyWebhook validates the signature over the raw body and parses the
	// event. It must reject any payload whose signature does not verify.
	VerifyWebhook(body []byte, headers http.Header) (WebhookEvent, error)
}
