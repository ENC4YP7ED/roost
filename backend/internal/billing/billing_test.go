package billing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"

	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

// ---- VAT ----

func TestComputeVATDomestic(t *testing.T) {
	// German seller, German consumer: 19% VAT on €100.00.
	r := ComputeVAT(10000, 1900, "DE", "DE", "")
	if r.NetCents != 10000 || r.VATCents != 1900 || r.GrossCents != 11900 {
		t.Errorf("domestic = %+v", r)
	}
	if r.ReverseCharge {
		t.Error("domestic sale marked reverse charge")
	}
}

func TestComputeVATReverseChargeEUBusiness(t *testing.T) {
	// German seller, French business with a VAT id: reverse charge, 0% VAT.
	r := ComputeVAT(10000, 1900, "DE", "FR", "FR12345678901")
	if !r.ReverseCharge {
		t.Fatal("EU B2B cross-border not marked reverse charge")
	}
	if r.VATCents != 0 || r.GrossCents != 10000 {
		t.Errorf("reverse charge should be 0 VAT: %+v", r)
	}
}

func TestComputeVATEUConsumerNoVATID(t *testing.T) {
	// German seller, French consumer (no VAT id): seller's rate still applies.
	r := ComputeVAT(10000, 1900, "DE", "FR", "")
	if r.ReverseCharge {
		t.Error("consumer sale must not be reverse charge")
	}
	if r.VATCents != 1900 {
		t.Errorf("EU consumer VAT = %d, want 1900", r.VATCents)
	}
}

func TestComputeVATExportOutsideEU(t *testing.T) {
	// German seller, US customer: no EU VAT.
	r := ComputeVAT(10000, 1900, "DE", "US", "")
	if r.VATCents != 0 || r.GrossCents != 10000 {
		t.Errorf("non-EU export should have no VAT: %+v", r)
	}
	if r.ReverseCharge {
		t.Error("non-EU export is not reverse charge")
	}
}

func TestComputeVATRounding(t *testing.T) {
	// 19% of €9.99 = 189.81 cents → rounds to 190.
	r := ComputeVAT(999, 1900, "DE", "DE", "")
	if r.VATCents != 190 {
		t.Errorf("VAT = %d, want 190 (round-half-up)", r.VATCents)
	}
	if r.GrossCents != 1189 {
		t.Errorf("gross = %d, want 1189", r.GrossCents)
	}
}

func TestComputeVATZeroRate(t *testing.T) {
	r := ComputeVAT(5000, 0, "DE", "DE", "")
	if r.VATCents != 0 || r.GrossCents != 5000 {
		t.Errorf("zero-rate = %+v", r)
	}
}

func TestComputeVATMissingCountryFallsBackToCharging(t *testing.T) {
	r := ComputeVAT(10000, 1900, "", "", "")
	if r.VATCents != 1900 {
		t.Errorf("with unknown countries we charge the configured rate: %+v", r)
	}
}

func TestIsEUCountry(t *testing.T) {
	for _, c := range []string{"DE", "de", " FR ", "IT"} {
		if !IsEUCountry(c) {
			t.Errorf("IsEUCountry(%q) = false", c)
		}
	}
	for _, c := range []string{"US", "GB", "CH", "", "XX"} {
		if IsEUCountry(c) {
			t.Errorf("IsEUCountry(%q) = true", c)
		}
	}
}

func TestFormatMoney(t *testing.T) {
	cases := []struct {
		cents    int64
		currency string
		want     string
	}{
		{1999, "EUR", "€19.99"},
		{100, "EUR", "€1.00"},
		{5, "EUR", "€0.05"},
		{0, "EUR", "€0.00"},
		{-500, "EUR", "-€5.00"},
		{2500, "USD", "$25.00"},
		{999, "GBP", "£9.99"},
		{1000, "CZK", "CZK 10.00"},
	}
	for _, tc := range cases {
		if got := FormatMoney(tc.cents, tc.currency); got != tc.want {
			t.Errorf("FormatMoney(%d, %q) = %q, want %q", tc.cents, tc.currency, got, tc.want)
		}
	}
}

// ---- Stripe ----

func stripeSignature(secret, body string, ts int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "." + body))
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func TestStripeVerifyWebhookValidSignature(t *testing.T) {
	s := NewStripe("sk_test", "whsec_test")
	body := `{"type":"checkout.session.completed","data":{"object":{"id":"cs_123","subscription":""}}}`
	sig := stripeSignature("whsec_test", body, time.Now().Unix())

	ev, err := s.VerifyWebhook([]byte(body), http.Header{"Stripe-Signature": {sig}})
	if err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if ev.Type != EventPaymentSucceeded || ev.OrderRef != "cs_123" {
		t.Errorf("event = %+v", ev)
	}
}

func TestStripeVerifyWebhookRejectsTamperedBody(t *testing.T) {
	s := NewStripe("sk_test", "whsec_test")
	body := `{"type":"checkout.session.completed","data":{"object":{"id":"cs_123"}}}`
	sig := stripeSignature("whsec_test", body, time.Now().Unix())

	// Same signature, different body.
	tampered := strings.Replace(body, "cs_123", "cs_evil", 1)
	if _, err := s.VerifyWebhook([]byte(tampered), http.Header{"Stripe-Signature": {sig}}); err == nil {
		t.Fatal("tampered body accepted")
	}
}

func TestStripeVerifyWebhookRejectsWrongSecret(t *testing.T) {
	s := NewStripe("sk_test", "whsec_real")
	body := `{"type":"checkout.session.completed","data":{"object":{"id":"cs_1"}}}`
	sig := stripeSignature("whsec_attacker", body, time.Now().Unix())
	if _, err := s.VerifyWebhook([]byte(body), http.Header{"Stripe-Signature": {sig}}); err == nil {
		t.Fatal("signature from a different secret accepted")
	}
}

func TestStripeVerifyWebhookRejectsOldTimestamp(t *testing.T) {
	s := NewStripe("sk_test", "whsec_test")
	body := `{"type":"x"}`
	sig := stripeSignature("whsec_test", body, time.Now().Add(-10*time.Minute).Unix())
	if _, err := s.VerifyWebhook([]byte(body), http.Header{"Stripe-Signature": {sig}}); err == nil {
		t.Fatal("replayed (old) signature accepted")
	}
}

func TestStripeVerifyWebhookMissingHeader(t *testing.T) {
	s := NewStripe("sk_test", "whsec_test")
	if _, err := s.VerifyWebhook([]byte(`{}`), http.Header{}); err == nil {
		t.Fatal("missing signature header accepted")
	}
}

func TestStripeParsesEventTypes(t *testing.T) {
	s := NewStripe("k", "whsec")
	now := time.Now().Unix()
	cases := []struct {
		body string
		want EventType
	}{
		{`{"type":"checkout.session.completed","data":{"object":{"id":"cs_1","subscription":"sub_1"}}}`, EventPaymentSucceeded},
		{`{"type":"invoice.paid","data":{"object":{"subscription":"sub_1","lines":{"data":[{"period":{"end":1893456000}}]}}}}`, EventSubscriptionPaid},
		{`{"type":"customer.subscription.deleted","data":{"object":{"id":"sub_1"}}}`, EventSubscriptionEnd},
		{`{"type":"charge.refunded","data":{"object":{"id":"ch_1"}}}`, EventRefunded},
		{`{"type":"payment_intent.payment_failed","data":{"object":{"id":"pi_1"}}}`, EventPaymentFailed},
		{`{"type":"customer.created","data":{"object":{}}}`, EventIgnored},
	}
	for _, tc := range cases {
		sig := stripeSignature("whsec", tc.body, now)
		ev, err := s.VerifyWebhook([]byte(tc.body), http.Header{"Stripe-Signature": {sig}})
		if err != nil {
			t.Fatalf("verify %q: %v", tc.body, err)
		}
		if ev.Type != tc.want {
			t.Errorf("event for %q = %q, want %q", tc.body, ev.Type, tc.want)
		}
	}
}

func TestStripeSubscriptionRenewalCarriesPeriodEnd(t *testing.T) {
	s := NewStripe("k", "whsec")
	body := `{"type":"invoice.paid","data":{"object":{"subscription":"sub_9","lines":{"data":[{"period":{"end":1893456000}}]}}}}`
	sig := stripeSignature("whsec", body, time.Now().Unix())
	ev, err := s.VerifyWebhook([]byte(body), http.Header{"Stripe-Signature": {sig}})
	if err != nil {
		t.Fatal(err)
	}
	if ev.SubscriptionID != "sub_9" || ev.PeriodEnd == "" {
		t.Errorf("renewal event = %+v", ev)
	}
}

func TestStripeCreateCheckout(t *testing.T) {
	var gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, _, _ := r.BasicAuth(); u != "sk_test" {
			t.Errorf("wrong auth user %q", u)
		}
		raw, _ := io.ReadAll(r.Body)
		gotForm = string(raw)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"cs_test_123","url":"https://checkout.stripe.com/pay/cs_test_123"}`))
	}))
	defer srv.Close()

	s := NewStripe("sk_test", "whsec")
	s.HTTP = srv.Client()
	// Point the client at the test server by overriding the transport host.
	s.HTTP.Transport = rewriteHost{srv.URL}

	res, err := s.CreateCheckout(CheckoutRequest{
		OrderUUID: "order-uuid", Description: "Paper Plan", AmountCents: 1999,
		Currency: "EUR", SuccessURL: "https://p/ok", CancelURL: "https://p/no",
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if res.ProviderRef != "cs_test_123" || !strings.Contains(res.RedirectURL, "checkout.stripe.com") {
		t.Errorf("result = %+v", res)
	}
	for _, want := range []string{"mode=payment", "unit_amount%5D=1999", "currency%5D=eur", "client_reference_id=order-uuid"} {
		if !strings.Contains(gotForm, want) {
			t.Errorf("checkout form missing %q; got %s", want, gotForm)
		}
	}
}

func TestStripeCheckoutSubscriptionMode(t *testing.T) {
	var gotForm string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		gotForm = string(raw)
		w.Write([]byte(`{"id":"cs_1","url":"https://x"}`))
	}))
	defer srv.Close()
	s := NewStripe("sk", "wh")
	s.HTTP = &http.Client{Transport: rewriteHost{srv.URL}}
	if _, err := s.CreateCheckout(CheckoutRequest{
		OrderUUID: "o", Description: "Monthly", AmountCents: 500, Currency: "EUR",
		Recurring: true, Interval: "month",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotForm, "mode=subscription") {
		t.Errorf("recurring checkout not in subscription mode: %s", gotForm)
	}
	if strings.Contains(gotForm, "payment_intent_data") {
		t.Error("payment_intent_data must not be sent in subscription mode")
	}
}

// ---- Revolut ----

func revolutSig(secret, body string, tsMillis int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("v1." + strconv.FormatInt(tsMillis, 10) + "." + body))
	return "v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestRevolutVerifyWebhookValid(t *testing.T) {
	r := NewRevolut("sk", "whsec", true)
	body := `{"event":"ORDER_COMPLETED","order_id":"6516e61e"}`
	ts := time.Now().UnixMilli()
	headers := http.Header{
		"Revolut-Signature":         {revolutSig("whsec", body, ts)},
		"Revolut-Request-Timestamp": {strconv.FormatInt(ts, 10)},
	}
	ev, err := r.VerifyWebhook([]byte(body), headers)
	if err != nil {
		t.Fatalf("valid signature rejected: %v", err)
	}
	if ev.Type != EventPaymentSucceeded || ev.OrderRef != "6516e61e" {
		t.Errorf("event = %+v", ev)
	}
}

func TestRevolutRejectsBadSignature(t *testing.T) {
	r := NewRevolut("sk", "whsec_real", true)
	body := `{"event":"ORDER_COMPLETED","order_id":"x"}`
	ts := time.Now().UnixMilli()
	headers := http.Header{
		"Revolut-Signature":         {revolutSig("whsec_attacker", body, ts)},
		"Revolut-Request-Timestamp": {strconv.FormatInt(ts, 10)},
	}
	if _, err := r.VerifyWebhook([]byte(body), headers); err == nil {
		t.Fatal("signature from wrong secret accepted")
	}
}

func TestRevolutRejectsTamperedBodyAndOldTimestamp(t *testing.T) {
	r := NewRevolut("sk", "whsec", true)
	body := `{"event":"ORDER_COMPLETED","order_id":"x"}`
	ts := time.Now().UnixMilli()
	sig := revolutSig("whsec", body, ts)

	tampered := strings.Replace(body, `"x"`, `"y"`, 1)
	if _, err := r.VerifyWebhook([]byte(tampered), http.Header{
		"Revolut-Signature": {sig}, "Revolut-Request-Timestamp": {strconv.FormatInt(ts, 10)},
	}); err == nil {
		t.Error("tampered body accepted")
	}

	old := time.Now().Add(-10 * time.Minute).UnixMilli()
	if _, err := r.VerifyWebhook([]byte(body), http.Header{
		"Revolut-Signature":         {revolutSig("whsec", body, old)},
		"Revolut-Request-Timestamp": {strconv.FormatInt(old, 10)},
	}); err == nil {
		t.Error("replayed old timestamp accepted")
	}
}

func TestRevolutMissingHeaders(t *testing.T) {
	r := NewRevolut("sk", "whsec", true)
	if _, err := r.VerifyWebhook([]byte(`{}`), http.Header{}); err == nil {
		t.Fatal("missing headers accepted")
	}
}

func TestRevolutEventMapping(t *testing.T) {
	r := NewRevolut("sk", "whsec", true)
	ts := time.Now().UnixMilli()
	cases := []struct {
		event string
		want  EventType
	}{
		{"ORDER_COMPLETED", EventPaymentSucceeded},
		{"ORDER_AUTHORISED", EventPaymentSucceeded},
		{"ORDER_CANCELLED", EventPaymentFailed},
		{"ORDER_PAYMENT_DECLINED", EventPaymentFailed},
		{"PAYMENT_REFUND_COMPLETED", EventRefunded},
		{"SOMETHING_ELSE", EventIgnored},
	}
	for _, tc := range cases {
		body := fmt.Sprintf(`{"event":%q,"order_id":"o1"}`, tc.event)
		headers := http.Header{
			"Revolut-Signature":         {revolutSig("whsec", body, ts)},
			"Revolut-Request-Timestamp": {strconv.FormatInt(ts, 10)},
		}
		ev, err := r.VerifyWebhook([]byte(body), headers)
		if err != nil {
			t.Fatalf("verify %q: %v", tc.event, err)
		}
		if ev.Type != tc.want {
			t.Errorf("%q → %q, want %q", tc.event, ev.Type, tc.want)
		}
	}
}

func TestRevolutCreateCheckout(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer sk_live" {
			t.Errorf("wrong auth: %q", req.Header.Get("Authorization"))
		}
		json.NewDecoder(req.Body).Decode(&gotBody)
		w.Write([]byte(`{"id":"ord_1","checkout_url":"https://checkout.revolut.com/pay/abc"}`))
	}))
	defer srv.Close()

	r := NewRevolut("sk_live", "wh", false)
	r.HTTP = &http.Client{Transport: rewriteHost{srv.URL}}
	res, err := r.CreateCheckout(CheckoutRequest{
		OrderUUID: "order-1", Description: "Plan", AmountCents: 2500, Currency: "EUR",
	})
	if err != nil {
		t.Fatalf("CreateCheckout: %v", err)
	}
	if res.ProviderRef != "ord_1" || !strings.Contains(res.RedirectURL, "checkout.revolut.com") {
		t.Errorf("result = %+v", res)
	}
	if gotBody["amount"] != float64(2500) || gotBody["merchant_order_ext_ref"] != "order-1" {
		t.Errorf("request body = %v", gotBody)
	}
}

func TestRevolutCheckoutSurfacesErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"invalid api key"}`))
	}))
	defer srv.Close()
	r := NewRevolut("bad", "wh", false)
	r.HTTP = &http.Client{Transport: rewriteHost{srv.URL}}
	if _, err := r.CreateCheckout(CheckoutRequest{OrderUUID: "o", Currency: "EUR", AmountCents: 1}); err == nil {
		t.Fatal("a 401 from Revolut did not produce an error")
	}
}

// rewriteHost redirects all requests to a test server, preserving the path.
type rewriteHost struct{ base string }

func (rw rewriteHost) RoundTrip(req *http.Request) (*http.Response, error) {
	target := rw.base + req.URL.Path
	if req.URL.RawQuery != "" {
		target += "?" + req.URL.RawQuery
	}
	newReq := req.Clone(req.Context())
	u := mustParse(target)
	newReq.URL = u
	newReq.Host = u.Host
	return http.DefaultTransport.RoundTrip(newReq)
}

func mustParse(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func TestIntervalLabelAndProviderNames(t *testing.T) {
	cases := map[string]string{"month": "per month", "year": "per year", "one_time": "one-time", "": "one-time"}
	for in, want := range cases {
		if got := IntervalLabel(in); got != want {
			t.Errorf("IntervalLabel(%q) = %q, want %q", in, got, want)
		}
	}
	if NewStripe("k", "w").Name() != "stripe" {
		t.Error("Stripe.Name")
	}
	if NewRevolut("k", "w", false).Name() != "revolut" {
		t.Error("Revolut.Name")
	}
	// baseURL sandbox vs prod.
	if NewRevolut("k", "w", true).baseURL() == NewRevolut("k", "w", false).baseURL() {
		t.Error("sandbox and prod base URLs should differ")
	}
}
