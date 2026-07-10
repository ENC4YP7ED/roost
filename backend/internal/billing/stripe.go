package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Stripe integrates Stripe Checkout. Payment happens on Stripe's hosted page;
// we create a Checkout Session, redirect the customer, and reconcile from the
// signed webhook. Only the Go standard library is used — the REST API is
// form-encoded and small enough not to warrant the SDK.
type Stripe struct {
	SecretKey     string
	WebhookSecret string
	HTTP          *http.Client
}

func NewStripe(secretKey, webhookSecret string) *Stripe {
	return &Stripe{
		SecretKey:     secretKey,
		WebhookSecret: webhookSecret,
		HTTP:          &http.Client{Timeout: 20 * time.Second},
	}
}

func (s *Stripe) Name() string { return "stripe" }

func (s *Stripe) CreateCheckout(req CheckoutRequest) (CheckoutResult, error) {
	form := url.Values{}
	form.Set("mode", stripeMode(req.Recurring))
	form.Set("success_url", req.SuccessURL)
	form.Set("cancel_url", req.CancelURL)
	form.Set("client_reference_id", req.OrderUUID)
	if req.Email != "" {
		form.Set("customer_email", req.Email)
	}
	// A single line item priced inline (price_data), so no dashboard setup.
	form.Set("line_items[0][quantity]", "1")
	form.Set("line_items[0][price_data][currency]", strings.ToLower(req.Currency))
	form.Set("line_items[0][price_data][unit_amount]", strconv.FormatInt(req.AmountCents, 10))
	form.Set("line_items[0][price_data][product_data][name]", req.Description)
	if req.Recurring {
		form.Set("line_items[0][price_data][recurring][interval]", req.Interval)
	}
	// Echo the order id back on the resulting object for the webhook.
	form.Set("metadata[order_uuid]", req.OrderUUID)
	form.Set("payment_intent_data[metadata][order_uuid]", req.OrderUUID)
	if req.Recurring {
		// payment_intent_data is invalid in subscription mode.
		form.Del("payment_intent_data[metadata][order_uuid]")
		form.Set("subscription_data[metadata][order_uuid]", req.OrderUUID)
	}

	var out struct {
		ID    string `json:"id"`
		URL   string `json:"url"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := s.post("/v1/checkout/sessions", form, &out); err != nil {
		return CheckoutResult{}, err
	}
	if out.Error != nil {
		return CheckoutResult{}, fmt.Errorf("stripe: %s", out.Error.Message)
	}
	if out.URL == "" {
		return CheckoutResult{}, fmt.Errorf("stripe returned no checkout url")
	}
	return CheckoutResult{RedirectURL: out.URL, ProviderRef: out.ID}, nil
}

func stripeMode(recurring bool) string {
	if recurring {
		return "subscription"
	}
	return "payment"
}

func (s *Stripe) post(path string, form url.Values, out any) error {
	req, err := http.NewRequest("POST", "https://api.stripe.com"+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(s.SecretKey, "")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("stripe unreachable: %w", err)
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("stripe: could not parse response (HTTP %d)", res.StatusCode)
	}
	return nil
}

// VerifyWebhook validates the Stripe-Signature header per Stripe's scheme:
// t=<timestamp>,v1=<hex hmac-sha256 of "<t>.<body>">.
func (s *Stripe) VerifyWebhook(body []byte, headers http.Header) (WebhookEvent, error) {
	sig := headers.Get("Stripe-Signature")
	if sig == "" {
		return WebhookEvent{}, fmt.Errorf("missing Stripe-Signature header")
	}
	var ts string
	var v1s []string
	for _, part := range strings.Split(sig, ",") {
		k, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			ts = val
		case "v1":
			v1s = append(v1s, val)
		}
	}
	if ts == "" || len(v1s) == 0 {
		return WebhookEvent{}, fmt.Errorf("malformed Stripe-Signature header")
	}
	// Reject signatures older than five minutes (replay protection).
	if n, err := strconv.ParseInt(ts, 10, 64); err == nil {
		if abs64(time.Now().Unix()-n) > 300 {
			return WebhookEvent{}, fmt.Errorf("stripe signature timestamp outside tolerance")
		}
	}
	mac := hmac.New(sha256.New, []byte(s.WebhookSecret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	expected := mac.Sum(nil)
	matched := false
	for _, v1 := range v1s {
		got, err := hex.DecodeString(v1)
		if err == nil && hmac.Equal(got, expected) {
			matched = true
			break
		}
	}
	if !matched {
		return WebhookEvent{}, fmt.Errorf("stripe signature verification failed")
	}
	return s.parse(body)
}

func (s *Stripe) parse(body []byte) (WebhookEvent, error) {
	var env struct {
		Type string `json:"type"`
		Data struct {
			Object json.RawMessage `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return WebhookEvent{}, fmt.Errorf("stripe: invalid event body")
	}

	switch env.Type {
	case "checkout.session.completed":
		var obj struct {
			ID            string `json:"id"`
			Mode          string `json:"mode"`
			Subscription  string `json:"subscription"`
			PaymentStatus string `json:"payment_status"`
		}
		json.Unmarshal(env.Data.Object, &obj)
		ev := WebhookEvent{Type: EventPaymentSucceeded, OrderRef: obj.ID, SubscriptionID: obj.Subscription}
		return ev, nil
	case "invoice.paid":
		// Recurring renewal.
		var obj struct {
			Subscription string `json:"subscription"`
			Lines        struct {
				Data []struct {
					Period struct {
						End int64 `json:"end"`
					} `json:"period"`
				} `json:"data"`
			} `json:"lines"`
		}
		json.Unmarshal(env.Data.Object, &obj)
		ev := WebhookEvent{Type: EventSubscriptionPaid, SubscriptionID: obj.Subscription}
		if len(obj.Lines.Data) > 0 && obj.Lines.Data[0].Period.End > 0 {
			ev.PeriodEnd = time.Unix(obj.Lines.Data[0].Period.End, 0).UTC().Format(time.RFC3339)
		}
		return ev, nil
	case "customer.subscription.deleted":
		var obj struct {
			ID string `json:"id"`
		}
		json.Unmarshal(env.Data.Object, &obj)
		return WebhookEvent{Type: EventSubscriptionEnd, SubscriptionID: obj.ID}, nil
	case "charge.refunded", "payment_intent.payment_failed":
		var obj struct {
			ID string `json:"id"`
		}
		json.Unmarshal(env.Data.Object, &obj)
		t := EventRefunded
		if env.Type == "payment_intent.payment_failed" {
			t = EventPaymentFailed
		}
		return WebhookEvent{Type: t, OrderRef: obj.ID}, nil
	}
	return WebhookEvent{Type: EventIgnored}, nil
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}
