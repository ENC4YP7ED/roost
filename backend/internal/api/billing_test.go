package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"roost/internal/store"
)

// enableBilling configures a German seller charging 19% VAT with Stripe on.
func (h *harness) enableBilling() {
	set := h.st.SetSetting
	set("billing:enabled", "1")
	set("billing:currency", "EUR")
	set("billing:vat_rate", "1900")
	set("billing:invoice_prefix", "INV")
	set("billing:seller_name", "Roost Hosting UG")
	set("billing:seller_country", "DE")
	set("billing:seller_vat_id", "DE123456789")
	set("billing:stripe_enabled", "1")
	set("billing:stripe_secret", "sk_test")
	set("billing:stripe_webhook_secret", "whsec_test")
}

func (h *harness) mkProduct(f fixture) *store.Product {
	h.t.Helper()
	p := &store.Product{
		Name: "Paper Plan", PriceCents: 1000, Currency: "EUR", BillingInterval: "one_time",
		EggID: f.egg.ID, Memory: 2048, Disk: 10240, IO: 500, Allocations: 1, Active: true,
	}
	if err := h.st.CreateProduct(p); err != nil {
		h.t.Fatal(err)
	}
	return p
}

func stripeSig(secret, body string) http.Header {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + body))
	return http.Header{"Stripe-Signature": {fmt.Sprintf("t=%s,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))}}
}

// doRaw posts a raw string body (webhooks are signed over exact bytes).
func (h *harness) doRaw(method, path, body string, headers http.Header) *response {
	h.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.10:1234"
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range headers {
		for _, v := range vs {
			req.Header.Set(k, v)
		}
	}
	rec := httptest.NewRecorder()
	h.h.ServeHTTP(rec, req)
	return &response{ResponseRecorder: rec, t: h.t}
}

// ---- settings ----

func TestBillingSettingsAdminOnlyAndSecretSafe(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()

	// Non-admin cannot read settings.
	ownerCookie := h.login("owner", "ownerpass1")
	if res := h.do("GET", "/api/application/billing/settings", nil, withCookie(ownerCookie)); res.Code != http.StatusForbidden {
		t.Errorf("owner got %d, want 403", res.Code)
	}

	adminCookie := h.login("admin", "adminpass1")
	save := h.do("PUT", "/api/application/billing/settings", map[string]any{
		"enabled": true, "currency": "eur", "vat_rate": 1900, "seller_name": "Acme",
		"stripe_enabled": true, "stripe_secret": "sk_live_supersecret",
		"stripe_webhook_secret": "whsec_x",
	}, withCookie(adminCookie))
	if save.Code != http.StatusOK {
		t.Fatalf("save = %d: %s", save.Code, save.Body.String())
	}
	// The response never echoes secrets.
	if strings.Contains(save.Body.String(), "sk_live_supersecret") || strings.Contains(save.Body.String(), "whsec_x") {
		t.Error("billing settings response leaked a secret")
	}
	body := save.json()
	if body["stripe_configured"] != true {
		t.Error("stripe_configured should be true once keys are set")
	}
	if body["currency"] != "EUR" {
		t.Errorf("currency not normalised: %v", body["currency"])
	}

	// Secrets persist and are usable, but omitting them on a later save keeps them.
	h.do("PUT", "/api/application/billing/settings", map[string]any{
		"enabled": true, "seller_name": "Acme", "vat_rate": 1900, "stripe_enabled": true,
	}, withCookie(adminCookie))
	if got := h.st.Setting("billing:stripe_secret", ""); got != "sk_live_supersecret" {
		t.Error("secret was wiped when the field was omitted")
	}
}

func TestBillingSettingsValidation(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("admin", "adminpass1")

	bad := h.do("PUT", "/api/application/billing/settings", map[string]any{
		"enabled": true, "seller_name": "Acme", "vat_rate": 20000}, withCookie(cookie))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("out-of-range VAT = %d, want 422", bad.Code)
	}
	noSeller := h.do("PUT", "/api/application/billing/settings", map[string]any{
		"enabled": true, "vat_rate": 1900}, withCookie(cookie))
	if noSeller.Code != http.StatusUnprocessableEntity {
		t.Errorf("enabling without a seller name = %d, want 422", noSeller.Code)
	}
}

// ---- products ----

func TestProductAdminCrudAndShopVisibility(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	adminCookie := h.login("admin", "adminpass1")

	create := h.do("POST", "/api/application/billing/products", map[string]any{
		"name": "Starter", "price_cents": 500, "currency": "EUR", "billing_interval": "month",
		"egg_id": f.egg.ID, "memory": 1024, "disk": 5120,
	}, withCookie(adminCookie))
	if create.Code != http.StatusOK {
		t.Fatalf("create product = %d: %s", create.Code, create.Body.String())
	}
	attrs := create.json()["attributes"].(map[string]any)
	id := int64(attrs["id"].(float64))
	if attrs["price"] != "€5.00" {
		t.Errorf("formatted price = %v", attrs["price"])
	}

	// Invalid interval.
	bad := h.do("POST", "/api/application/billing/products", map[string]any{
		"name": "X", "price_cents": 1, "egg_id": f.egg.ID, "billing_interval": "weekly"}, withCookie(adminCookie))
	if bad.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad interval = %d, want 422", bad.Code)
	}
	// Unknown egg.
	badEgg := h.do("POST", "/api/application/billing/products", map[string]any{
		"name": "X", "price_cents": 1, "egg_id": 9999}, withCookie(adminCookie))
	if badEgg.Code != http.StatusUnprocessableEntity {
		t.Errorf("unknown egg = %d, want 422", badEgg.Code)
	}

	// The shop shows active products to a normal customer.
	ownerCookie := h.login("owner", "ownerpass1")
	shop := h.do("GET", "/api/client/billing/products", nil, withCookie(ownerCookie))
	if shop.Code != http.StatusOK || len(shop.json()["data"].([]any)) != 1 {
		t.Fatalf("shop shows %d products", len(shop.json()["data"].([]any)))
	}

	// Deactivate → disappears from the shop but stays in admin.
	h.do("PATCH", fmt.Sprintf("/api/application/billing/products/%d", id),
		map[string]any{"active": false}, withCookie(adminCookie))
	shop = h.do("GET", "/api/client/billing/products", nil, withCookie(ownerCookie))
	if len(shop.json()["data"].([]any)) != 0 {
		t.Error("deactivated product still shown in the shop")
	}
	adminList := h.do("GET", "/api/application/billing/products", nil, withCookie(adminCookie))
	if len(adminList.json()["data"].([]any)) != 1 {
		t.Error("admin product list should include inactive products")
	}
}

func TestShopHiddenWhenBillingDisabled(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.mkProduct(f) // exists, but billing is off
	cookie := h.login("owner", "ownerpass1")
	res := h.do("GET", "/api/client/billing/products", nil, withCookie(cookie))
	if len(res.json()["data"].([]any)) != 0 {
		t.Error("products exposed while billing is disabled")
	}
}

// ---- billing profile & VAT at checkout ----

func TestBillingProfileRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	cookie := h.login("owner", "ownerpass1")

	put := h.do("PUT", "/api/client/billing/profile", map[string]any{
		"Company": "Acme SAS", "Country": "fr", "VATID": "FR12345678901", "City": "Paris",
	}, withCookie(cookie))
	if put.Code != http.StatusOK {
		t.Fatalf("save profile = %d: %s", put.Code, put.Body.String())
	}
	attrs := put.json()["attributes"].(map[string]any)
	if attrs["country"] != "FR" {
		t.Errorf("country not upper-cased: %v", attrs["country"])
	}
	get := h.do("GET", "/api/client/billing/profile", nil, withCookie(cookie))
	if get.json()["attributes"].(map[string]any)["vat_id"] != "FR12345678901" {
		t.Error("profile did not persist")
	}
}

func TestCheckoutRequiresEnabledProvider(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	p := h.mkProduct(f)
	cookie := h.login("owner", "ownerpass1")

	// Revolut is not configured.
	res := h.do("POST", "/api/client/billing/checkout", map[string]any{
		"product_id": p.ID, "provider": "revolut"}, withCookie(cookie))
	if res.Code != http.StatusBadRequest {
		t.Errorf("checkout with a disabled provider = %d, want 400", res.Code)
	}
	// Unknown product.
	missing := h.do("POST", "/api/client/billing/checkout", map[string]any{
		"product_id": 9999, "provider": "stripe"}, withCookie(cookie))
	if missing.Code != http.StatusNotFound {
		t.Errorf("unknown product = %d, want 404", missing.Code)
	}
}

func TestCheckoutCreatesPendingOrderWithVAT(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	p := h.mkProduct(f) // €10.00 net
	cookie := h.login("owner", "ownerpass1")

	// Domestic customer (DE): 19% VAT applies.
	h.do("PUT", "/api/client/billing/profile", map[string]any{"Country": "DE"}, withCookie(cookie))

	// Checkout will fail to reach Stripe (no network), but the order must be
	// created with the right VAT breakdown first, then marked failed.
	res := h.do("POST", "/api/client/billing/checkout", map[string]any{
		"product_id": p.ID, "provider": "stripe"}, withCookie(cookie))
	// 502 because api.stripe.com is unreachable in tests; the order still exists.
	if res.Code != http.StatusBadGateway {
		t.Logf("checkout status %d (expected 502 offline): %s", res.Code, res.Body.String())
	}
	orders, _ := h.st.OrdersForUser(f.owner.ID)
	if len(orders) != 1 {
		t.Fatalf("orders created = %d, want 1", len(orders))
	}
	o := orders[0]
	if o.NetCents != 1000 || o.VATCents != 190 || o.GrossCents != 1190 {
		t.Errorf("VAT breakdown wrong: net=%d vat=%d gross=%d", o.NetCents, o.VATCents, o.GrossCents)
	}
	if o.ReverseCharge {
		t.Error("domestic order marked reverse charge")
	}
}

func TestCheckoutReverseChargeForEUBusiness(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	p := h.mkProduct(f)
	cookie := h.login("owner", "ownerpass1")
	h.do("PUT", "/api/client/billing/profile", map[string]any{"Country": "FR", "VATID": "FR123"}, withCookie(cookie))

	h.do("POST", "/api/client/billing/checkout", map[string]any{"product_id": p.ID, "provider": "stripe"}, withCookie(cookie))
	orders, _ := h.st.OrdersForUser(f.owner.ID)
	o := orders[0]
	if !o.ReverseCharge || o.VATCents != 0 || o.GrossCents != 1000 {
		t.Errorf("EU B2B should be reverse-charged: %+v", o)
	}
}

// ---- webhook fulfilment: the core flow ----

func TestStripeWebhookProvisionsServerAndIssuesInvoice(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	p := h.mkProduct(f)

	// A pending order the customer already started (as if via checkout).
	order := &store.Order{
		UUID: "order-abc", UserID: f.owner.ID, ProductID: p.ID, Provider: "stripe",
		ProviderRef: "cs_test_1", Status: "pending", NetCents: 1000, VATCents: 190,
		GrossCents: 1190, VATRate: 1900, Currency: "EUR",
	}
	if err := h.st.CreateOrder(order); err != nil {
		t.Fatal(err)
	}
	serversBefore, _ := h.st.CountServers()

	body := `{"type":"checkout.session.completed","data":{"object":{"id":"cs_test_1","subscription":""}}}`
	res := h.doRaw("POST", "/api/billing/webhook/stripe", body, stripeSig("whsec_test", body))
	if res.Code != http.StatusNoContent {
		t.Fatalf("webhook = %d: %s", res.Code, res.Body.String())
	}

	// Order is now paid and linked to a server.
	got, _ := h.st.OrderByUUID("order-abc")
	if got.Status != "paid" || got.PaidAt == nil {
		t.Fatalf("order not marked paid: %+v", got)
	}
	if got.ServerID == nil {
		t.Fatal("no server provisioned for the paid order")
	}
	serversAfter, _ := h.st.CountServers()
	if serversAfter != serversBefore+1 {
		t.Errorf("server count %d → %d, want +1", serversBefore, serversAfter)
	}
	srv, _ := h.st.ServerByID(*got.ServerID)
	if srv.OwnerID != f.owner.ID || srv.Memory != 2048 || srv.EggID != f.egg.ID {
		t.Errorf("provisioned server does not match the product: %+v", srv)
	}
	if srv.Status == nil || *srv.Status != "installing" {
		t.Error("provisioned server should be installing")
	}

	// An invoice was issued with the right totals.
	inv, err := h.st.InvoiceByOrder(got.ID)
	if err != nil {
		t.Fatalf("no invoice issued: %v", err)
	}
	if inv.GrossCents != 1190 || inv.VATCents != 190 || !strings.HasPrefix(inv.Number, "INV-") {
		t.Errorf("invoice wrong: %+v", inv)
	}
	if inv.Status != "paid" {
		t.Errorf("invoice status = %q, want paid", inv.Status)
	}

	// The customer can see the order and invoice.
	cookie := h.login("owner", "ownerpass1")
	orders := h.do("GET", "/api/client/billing/orders", nil, withCookie(cookie))
	if len(orders.json()["data"].([]any)) != 1 {
		t.Error("customer cannot see their order")
	}
	invoices := h.do("GET", "/api/client/billing/invoices", nil, withCookie(cookie))
	if len(invoices.json()["data"].([]any)) != 1 {
		t.Error("customer cannot see their invoice")
	}

	// The invoice HTML renders and carries the number + a total.
	htmlRes := h.do("GET", "/api/client/billing/invoices/"+inv.Number+"/html", nil, withCookie(cookie))
	if htmlRes.Code != http.StatusOK {
		t.Fatalf("invoice html = %d", htmlRes.Code)
	}
	page := htmlRes.Body.String()
	if !strings.Contains(page, inv.Number) || !strings.Contains(page, "€11.90") {
		t.Error("invoice HTML missing the number or total")
	}
	if !strings.Contains(page, "Roost Hosting UG") {
		t.Error("invoice HTML missing the seller")
	}
}

func TestStripeWebhookIsIdempotent(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	p := h.mkProduct(f)
	h.st.CreateOrder(&store.Order{UUID: "o", UserID: f.owner.ID, ProductID: p.ID,
		Provider: "stripe", ProviderRef: "cs_dup", Status: "pending",
		NetCents: 1000, VATCents: 190, GrossCents: 1190, Currency: "EUR"})

	body := `{"type":"checkout.session.completed","data":{"object":{"id":"cs_dup"}}}`
	h.doRaw("POST", "/api/billing/webhook/stripe", body, stripeSig("whsec_test", body))
	// Redeliver the same event.
	h.doRaw("POST", "/api/billing/webhook/stripe", body, stripeSig("whsec_test", body))

	count, _ := h.st.CountServers()
	// Fixture created one server; fulfilment should add exactly one, not two.
	if count != 2 {
		t.Errorf("redelivered webhook provisioned twice: %d servers", count)
	}
	invoices, _ := h.st.Invoices()
	if len(invoices) != 1 {
		t.Errorf("redelivered webhook issued %d invoices, want 1", len(invoices))
	}
}

func TestWebhookRejectsBadSignature(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	h.enableBilling()

	body := `{"type":"checkout.session.completed","data":{"object":{"id":"cs_x"}}}`
	// Signed with the wrong secret.
	res := h.doRaw("POST", "/api/billing/webhook/stripe", body, stripeSig("whsec_attacker", body))
	if res.Code != http.StatusBadRequest {
		t.Errorf("forged webhook = %d, want 400", res.Code)
	}
	// No signature at all.
	none := h.doRaw("POST", "/api/billing/webhook/stripe", body, http.Header{})
	if none.Code != http.StatusBadRequest {
		t.Errorf("unsigned webhook = %d, want 400", none.Code)
	}
}

func TestWebhookUnknownProviderRefIsNoOp(t *testing.T) {
	h := newHarness(t)
	h.seedFixture()
	h.enableBilling()
	body := `{"type":"checkout.session.completed","data":{"object":{"id":"cs_ghost"}}}`
	res := h.doRaw("POST", "/api/billing/webhook/stripe", body, stripeSig("whsec_test", body))
	// Verified fine, just nothing to fulfil.
	if res.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", res.Code)
	}
}

func TestRefundSuspendsServer(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	p := h.mkProduct(f)
	order := &store.Order{UUID: "o", UserID: f.owner.ID, ProductID: p.ID, Provider: "stripe",
		ProviderRef: "cs_refund", Status: "paid", NetCents: 1000, GrossCents: 1190, Currency: "EUR",
		ServerID: &f.server.ID}
	h.st.CreateOrder(order)

	body := `{"type":"charge.refunded","data":{"object":{"id":"cs_refund"}}}`
	res := h.doRaw("POST", "/api/billing/webhook/stripe", body, stripeSig("whsec_test", body))
	if res.Code != http.StatusNoContent {
		t.Fatal(res.Code)
	}
	got, _ := h.st.OrderByUUID("o")
	if got.Status != "refunded" {
		t.Errorf("order status = %q, want refunded", got.Status)
	}
	srv, _ := h.st.ServerByID(f.server.ID)
	if srv.Status == nil || *srv.Status != "suspended" {
		t.Error("refund did not suspend the server")
	}
}

func TestSubscriptionCancellationSuspendsServer(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	p := h.mkProduct(f)
	sub := &store.Subscription{UUID: "s", UserID: f.owner.ID, ProductID: p.ID, Provider: "stripe",
		ProviderRef: "sub_1", Status: "active", ServerID: &f.server.ID}
	h.st.CreateSubscription(sub)

	body := `{"type":"customer.subscription.deleted","data":{"object":{"id":"sub_1"}}}`
	res := h.doRaw("POST", "/api/billing/webhook/stripe", body, stripeSig("whsec_test", body))
	if res.Code != http.StatusNoContent {
		t.Fatal(res.Code)
	}
	got, _ := h.st.SubscriptionByProviderRef("stripe", "sub_1")
	if got.Status != "canceled" {
		t.Errorf("subscription status = %q, want canceled", got.Status)
	}
	srv, _ := h.st.ServerByID(f.server.ID)
	if srv.Status == nil || *srv.Status != "suspended" {
		t.Error("cancellation did not suspend the linked server")
	}
}

func TestSubscriptionRenewalUnsuspendsServer(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	p := h.mkProduct(f)
	suspended := "suspended"
	f.server.Status = &suspended
	h.st.UpdateServer(f.server)
	sub := &store.Subscription{UUID: "s", UserID: f.owner.ID, ProductID: p.ID, Provider: "stripe",
		ProviderRef: "sub_2", Status: "past_due", ServerID: &f.server.ID}
	h.st.CreateSubscription(sub)

	body := `{"type":"invoice.paid","data":{"object":{"subscription":"sub_2","lines":{"data":[{"period":{"end":1893456000}}]}}}}`
	res := h.doRaw("POST", "/api/billing/webhook/stripe", body, stripeSig("whsec_test", body))
	if res.Code != http.StatusNoContent {
		t.Fatal(res.Code)
	}
	got, _ := h.st.SubscriptionByProviderRef("stripe", "sub_2")
	if got.Status != "active" || got.CurrentPeriodEnd == nil {
		t.Errorf("renewal not recorded: %+v", got)
	}
	srv, _ := h.st.ServerByID(f.server.ID)
	if srv.Status != nil {
		t.Error("paid renewal did not lift the suspension")
	}
}

func TestInvoiceAccessControl(t *testing.T) {
	h := newHarness(t)
	f := h.seedFixture()
	h.enableBilling()
	// Issue an invoice for the owner.
	inv := &store.Invoice{UserID: f.owner.ID, Status: "paid", Currency: "EUR",
		NetCents: 1000, VATCents: 190, GrossCents: 1190, Seller: "{}", Buyer: "{}", Lines: "[]",
		IssuedAt: nowISO()}
	h.st.CreateInvoice(inv, "INV")

	// A different user must not read it.
	stranger := h.mkUser("stranger", "s@e.com", "strangerpass", false)
	_ = stranger
	strangerCookie := h.login("stranger", "strangerpass")
	res := h.do("GET", "/api/client/billing/invoices/"+inv.Number+"/html", nil, withCookie(strangerCookie))
	if res.Code != http.StatusNotFound {
		t.Errorf("cross-user invoice access = %d, want 404", res.Code)
	}
}

func TestBillingUnauthenticated(t *testing.T) {
	h := newHarness(t)
	for _, p := range []string{
		"/api/client/billing/products", "/api/client/billing/orders",
		"/api/client/billing/invoices", "/api/application/billing/settings",
		"/api/application/billing/products", "/api/application/billing/orders",
	} {
		if res := h.do("GET", p, nil); res.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", p, res.Code)
		}
	}
}
