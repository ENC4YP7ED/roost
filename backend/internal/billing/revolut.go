package billing

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Revolut integrates the Revolut Business Merchant API. We create an order,
// send the customer to the hosted checkout, and reconcile from the signed
// webhook. Amounts are minor units, matching our storage.
type Revolut struct {
	SecretKey     string
	WebhookSecret string
	Sandbox       bool
	HTTP          *http.Client
}

func NewRevolut(secretKey, webhookSecret string, sandbox bool) *Revolut {
	return &Revolut{
		SecretKey:     secretKey,
		WebhookSecret: webhookSecret,
		Sandbox:       sandbox,
		HTTP:          &http.Client{Timeout: 20 * time.Second},
	}
}

func (r *Revolut) Name() string { return "revolut" }

func (r *Revolut) baseURL() string {
	if r.Sandbox {
		return "https://sandbox-merchant.revolut.com"
	}
	return "https://merchant.revolut.com"
}

func (r *Revolut) CreateCheckout(req CheckoutRequest) (CheckoutResult, error) {
	// Revolut's Merchant API takes the amount in minor units already.
	payload := map[string]any{
		"amount":                 req.AmountCents,
		"currency":               strings.ToUpper(req.Currency),
		"description":            req.Description,
		"merchant_order_ext_ref": req.OrderUUID,
		"redirect_url":           req.SuccessURL,
	}
	if req.Email != "" {
		payload["customer"] = map[string]any{"email": req.Email}
	}

	var out struct {
		ID          string `json:"id"`
		PublicID    string `json:"public_id"`
		CheckoutURL string `json:"checkout_url"`
		HostedURL   string `json:"hosted_payment_page_url"`
		Message     string `json:"message"`
	}
	if err := r.post("/api/1.0/orders", payload, &out); err != nil {
		return CheckoutResult{}, err
	}
	if out.ID == "" {
		return CheckoutResult{}, fmt.Errorf("revolut: %s", firstNonEmpty(out.Message, "no order id returned"))
	}
	redirect := firstNonEmpty(out.CheckoutURL, out.HostedURL)
	if redirect == "" && out.PublicID != "" {
		// Fall back to the standard hosted-page URL built from the public id.
		host := "https://checkout.revolut.com"
		if r.Sandbox {
			host = "https://sandbox-checkout.revolut.com"
		}
		redirect = fmt.Sprintf("%s/payment-link/%s", host, out.PublicID)
	}
	if redirect == "" {
		return CheckoutResult{}, fmt.Errorf("revolut returned no checkout url")
	}
	return CheckoutResult{RedirectURL: redirect, ProviderRef: out.ID}, nil
}

func (r *Revolut) post(path string, payload any, out any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequest("POST", r.baseURL()+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.SecretKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := r.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("revolut unreachable: %w", err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("revolut responded with HTTP %d: %s", res.StatusCode, truncate(string(body), 200))
	}
	return json.Unmarshal(body, out)
}

// VerifyWebhook validates Revolut's signature scheme:
//
//	Revolut-Signature: v1=<hex hmac-sha256>
//	Revolut-Request-Timestamp: <unix ms>
//
// where the signed payload is "v1.<timestamp>.<raw body>".
func (r *Revolut) VerifyWebhook(body []byte, headers http.Header) (WebhookEvent, error) {
	sigHeader := headers.Get("Revolut-Signature")
	ts := headers.Get("Revolut-Request-Timestamp")
	if sigHeader == "" || ts == "" {
		return WebhookEvent{}, fmt.Errorf("missing Revolut signature headers")
	}
	// Replay protection: reject timestamps far from now (header is in ms).
	if ms, err := strconv.ParseInt(ts, 10, 64); err == nil {
		if abs64(time.Now().UnixMilli()-ms) > 5*60*1000 {
			return WebhookEvent{}, fmt.Errorf("revolut signature timestamp outside tolerance")
		}
	}

	mac := hmac.New(sha256.New, []byte(r.WebhookSecret))
	mac.Write([]byte("v1." + ts + "."))
	mac.Write(body)
	expected := "v1=" + hex.EncodeToString(mac.Sum(nil))

	matched := false
	for _, candidate := range strings.Fields(sigHeader) {
		if hmac.Equal([]byte(candidate), []byte(expected)) {
			matched = true
			break
		}
	}
	if !matched {
		return WebhookEvent{}, fmt.Errorf("revolut signature verification failed")
	}
	return r.parse(body)
}

func (r *Revolut) parse(body []byte) (WebhookEvent, error) {
	var env struct {
		Event   string `json:"event"`
		OrderID string `json:"order_id"`
		Data    struct {
			ID    string `json:"id"`
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return WebhookEvent{}, fmt.Errorf("revolut: invalid event body")
	}
	ref := firstNonEmpty(env.OrderID, env.Data.ID)
	switch env.Event {
	case "ORDER_COMPLETED":
		return WebhookEvent{Type: EventPaymentSucceeded, OrderRef: ref}, nil
	case "ORDER_AUTHORISED":
		// Authorised but not captured — treat as success for auto-capture setups.
		return WebhookEvent{Type: EventPaymentSucceeded, OrderRef: ref}, nil
	case "ORDER_CANCELLED", "ORDER_PAYMENT_DECLINED", "ORDER_PAYMENT_FAILED":
		return WebhookEvent{Type: EventPaymentFailed, OrderRef: ref}, nil
	case "PAYMENT_REFUND_COMPLETED":
		return WebhookEvent{Type: EventRefunded, OrderRef: ref}, nil
	}
	return WebhookEvent{Type: EventIgnored}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
