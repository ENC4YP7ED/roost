package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"roost/internal/auth"
	"roost/internal/billing"
	"roost/internal/store"
	"roost/internal/wings"
)

// ---- settings ----

// BillingSettings is the panel's payment configuration, stored in the settings
// table. Secrets are never returned to the browser.
type BillingSettings struct {
	Enabled       bool   `json:"enabled"`
	Currency      string `json:"currency"`
	VATRate       int64  `json:"vat_rate"` // basis points
	InvoicePrefix string `json:"invoice_prefix"`

	SellerName    string `json:"seller_name"`
	SellerAddress string `json:"seller_address"`
	SellerCountry string `json:"seller_country"`
	SellerVATID   string `json:"seller_vat_id"`
	SellerEmail   string `json:"seller_email"`

	StripeEnabled     bool   `json:"stripe_enabled"`
	RevolutEnabled    bool   `json:"revolut_enabled"`
	RevolutSandbox    bool   `json:"revolut_sandbox"`
	StripeConfigured  bool   `json:"stripe_configured"`
	RevolutConfigured bool   `json:"revolut_configured"`
	StripePublishable string `json:"stripe_publishable"`
}

func (a *API) billingSettings() BillingSettings {
	get := func(k, d string) string { return a.Store.Setting(k, d) }
	rate, _ := strconv.ParseInt(get("billing:vat_rate", "0"), 10, 64)
	return BillingSettings{
		Enabled:           get("billing:enabled", "") == "1",
		Currency:          get("billing:currency", "EUR"),
		VATRate:           rate,
		InvoicePrefix:     get("billing:invoice_prefix", "INV"),
		SellerName:        get("billing:seller_name", ""),
		SellerAddress:     get("billing:seller_address", ""),
		SellerCountry:     get("billing:seller_country", ""),
		SellerVATID:       get("billing:seller_vat_id", ""),
		SellerEmail:       get("billing:seller_email", ""),
		StripeEnabled:     get("billing:stripe_enabled", "") == "1",
		RevolutEnabled:    get("billing:revolut_enabled", "") == "1",
		RevolutSandbox:    get("billing:revolut_sandbox", "") == "1",
		StripeConfigured:  get("billing:stripe_secret", "") != "" && get("billing:stripe_webhook_secret", "") != "",
		RevolutConfigured: get("billing:revolut_secret", "") != "" && get("billing:revolut_webhook_secret", "") != "",
		StripePublishable: get("billing:stripe_publishable", ""),
	}
}

// provider builds a billing.Provider by name from stored settings, or nil.
func (a *API) provider(name string) billing.Provider {
	get := func(k string) string { return a.Store.Setting(k, "") }
	switch name {
	case "stripe":
		if get("billing:stripe_enabled") != "1" || get("billing:stripe_secret") == "" {
			return nil
		}
		return billing.NewStripe(get("billing:stripe_secret"), get("billing:stripe_webhook_secret"))
	case "revolut":
		if get("billing:revolut_enabled") != "1" || get("billing:revolut_secret") == "" {
			return nil
		}
		return billing.NewRevolut(get("billing:revolut_secret"), get("billing:revolut_webhook_secret"),
			get("billing:revolut_sandbox") == "1")
	}
	return nil
}

// ---- transformers ----

func trProduct(p *store.Product) map[string]any {
	return map[string]any{
		"id":               p.ID,
		"name":             p.Name,
		"description":      p.Description,
		"price_cents":      p.PriceCents,
		"price":            billing.FormatMoney(p.PriceCents, p.Currency),
		"currency":         p.Currency,
		"billing_interval": p.BillingInterval,
		"interval_label":   billing.IntervalLabel(p.BillingInterval),
		"egg_id":           p.EggID,
		"node_id":          p.NodeID,
		"docker_image":     p.DockerImage,
		"limits": map[string]any{
			"memory": p.Memory, "swap": p.Swap, "disk": p.Disk, "io": p.IO, "cpu": p.CPU,
		},
		"feature_limits": map[string]any{
			"databases": p.Databases, "allocations": p.Allocations, "backups": p.Backups,
		},
		"active":     p.Active,
		"sort":       p.Sort,
		"created_at": p.CreatedAt,
	}
}

func trOrder(o *store.Order) map[string]any {
	return map[string]any{
		"uuid":           o.UUID,
		"product_id":     o.ProductID,
		"provider":       o.Provider,
		"status":         o.Status,
		"net_cents":      o.NetCents,
		"vat_cents":      o.VATCents,
		"gross_cents":    o.GrossCents,
		"gross":          billing.FormatMoney(o.GrossCents, o.Currency),
		"vat_rate":       o.VATRate,
		"reverse_charge": o.ReverseCharge,
		"currency":       o.Currency,
		"server_id":      o.ServerID,
		"created_at":     o.CreatedAt,
		"paid_at":        o.PaidAt,
	}
}

func trInvoice(v *store.Invoice) map[string]any {
	return map[string]any{
		"id":             v.ID,
		"number":         v.Number,
		"status":         v.Status,
		"currency":       v.Currency,
		"net_cents":      v.NetCents,
		"vat_cents":      v.VATCents,
		"gross_cents":    v.GrossCents,
		"gross":          billing.FormatMoney(v.GrossCents, v.Currency),
		"vat_rate":       v.VATRate,
		"reverse_charge": v.ReverseCharge,
		"seller":         json.RawMessage(v.Seller),
		"buyer":          json.RawMessage(v.Buyer),
		"lines":          json.RawMessage(v.Lines),
		"notes":          v.Notes,
		"issued_at":      v.IssuedAt,
		"due_at":         v.DueAt,
		"paid_at":        v.PaidAt,
	}
}

func trSubscription(s *store.Subscription) map[string]any {
	return map[string]any{
		"uuid":               s.UUID,
		"product_id":         s.ProductID,
		"provider":           s.Provider,
		"status":             s.Status,
		"server_id":          s.ServerID,
		"current_period_end": s.CurrentPeriodEnd,
		"created_at":         s.CreatedAt,
	}
}

// ---- routes ----

func (a *API) routesBilling(mux *http.ServeMux) {
	admin := a.requireAdmin
	user := a.requireUser

	// ---- public-ish (customer) ----
	// The shop lists active products for any signed-in user.
	mux.HandleFunc("GET /api/client/billing/products", user(func(w http.ResponseWriter, r *http.Request) {
		if !a.billingSettings().Enabled {
			writeList(w, r, "product", nil)
			return
		}
		products, _ := a.Store.Products(true)
		rows := make([]map[string]any, 0, len(products))
		for _, p := range products {
			rows = append(rows, trProduct(p))
		}
		writeList(w, r, "product", rows)
	}))

	mux.HandleFunc("GET /api/client/billing/profile", user(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.Store.BillingProfile(userFrom(r).ID)
		if err != nil {
			writeItem(w, http.StatusOK, "billing_profile", map[string]any{})
			return
		}
		writeItem(w, http.StatusOK, "billing_profile", trProfile(p))
	}))

	mux.HandleFunc("PUT /api/client/billing/profile", user(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name, Company, Address, City, PostalCode, Country, VATID string
		}
		if err := decode(r, &body); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
			return
		}
		p := &store.BillingProfile{
			UserID: userFrom(r).ID, Name: body.Name, Company: body.Company, Address: body.Address,
			City: body.City, PostalCode: strings.TrimSpace(body.PostalCode),
			Country: strings.ToUpper(strings.TrimSpace(body.Country)), VATID: strings.TrimSpace(body.VATID),
		}
		if err := a.Store.UpsertBillingProfile(p); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeItem(w, http.StatusOK, "billing_profile", trProfile(p))
	}))

	mux.HandleFunc("POST /api/client/billing/checkout", user(a.handleCheckout))

	mux.HandleFunc("GET /api/client/billing/orders", user(func(w http.ResponseWriter, r *http.Request) {
		orders, _ := a.Store.OrdersForUser(userFrom(r).ID)
		rows := make([]map[string]any, 0, len(orders))
		for _, o := range orders {
			rows = append(rows, trOrder(o))
		}
		writeList(w, r, "order", rows)
	}))

	mux.HandleFunc("GET /api/client/billing/subscriptions", user(func(w http.ResponseWriter, r *http.Request) {
		subs, _ := a.Store.SubscriptionsForUser(userFrom(r).ID)
		rows := make([]map[string]any, 0, len(subs))
		for _, s := range subs {
			rows = append(rows, trSubscription(s))
		}
		writeList(w, r, "subscription", rows)
	}))

	mux.HandleFunc("GET /api/client/billing/invoices", user(func(w http.ResponseWriter, r *http.Request) {
		invoices, _ := a.Store.InvoicesForUser(userFrom(r).ID)
		rows := make([]map[string]any, 0, len(invoices))
		for _, v := range invoices {
			rows = append(rows, trInvoice(v))
		}
		writeList(w, r, "invoice", rows)
	}))

	// Invoice as a printable HTML document (the browser can print-to-PDF).
	mux.HandleFunc("GET /api/client/billing/invoices/{number}/html", user(func(w http.ResponseWriter, r *http.Request) {
		inv, err := a.Store.InvoiceByNumber(r.PathValue("number"))
		if err != nil || inv.UserID != userFrom(r).ID {
			writeError(w, http.StatusNotFound, "Invoice not found.")
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(renderInvoiceHTML(inv, a.AppName())))
	}))

	// ---- webhooks (no auth; signature-verified) ----
	mux.HandleFunc("POST /api/billing/webhook/stripe", a.webhookHandler("stripe"))
	mux.HandleFunc("POST /api/billing/webhook/revolut", a.webhookHandler("revolut"))

	// ---- admin ----
	mux.HandleFunc("GET /api/application/billing/settings", admin(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.billingSettings())
	}))
	mux.HandleFunc("PUT /api/application/billing/settings", admin(a.handleSaveBillingSettings))

	mux.HandleFunc("GET /api/application/billing/products", admin(func(w http.ResponseWriter, r *http.Request) {
		products, _ := a.Store.Products(false)
		rows := make([]map[string]any, 0, len(products))
		for _, p := range products {
			rows = append(rows, trProduct(p))
		}
		writeList(w, r, "product", rows)
	}))
	mux.HandleFunc("POST /api/application/billing/products", admin(func(w http.ResponseWriter, r *http.Request) {
		a.upsertProduct(w, r, nil)
	}))
	mux.HandleFunc("PATCH /api/application/billing/products/{id}", admin(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.Store.ProductByID(parseID(r, "id"))
		if err != nil {
			writeError(w, http.StatusNotFound, "Product not found.")
			return
		}
		a.upsertProduct(w, r, p)
	}))
	mux.HandleFunc("DELETE /api/application/billing/products/{id}", admin(func(w http.ResponseWriter, r *http.Request) {
		if err := a.Store.DeleteProduct(parseID(r, "id")); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeNoContent(w)
	}))

	mux.HandleFunc("GET /api/application/billing/orders", admin(func(w http.ResponseWriter, r *http.Request) {
		orders, _ := a.Store.Orders()
		rows := make([]map[string]any, 0, len(orders))
		for _, o := range orders {
			row := trOrder(o)
			row["user_id"] = o.UserID
			rows = append(rows, row)
		}
		writeList(w, r, "order", rows)
	}))

	mux.HandleFunc("GET /api/application/billing/invoices", admin(func(w http.ResponseWriter, r *http.Request) {
		invoices, _ := a.Store.Invoices()
		rows := make([]map[string]any, 0, len(invoices))
		for _, v := range invoices {
			row := trInvoice(v)
			row["user_id"] = v.UserID
			rows = append(rows, row)
		}
		writeList(w, r, "invoice", rows)
	}))
}

func trProfile(p *store.BillingProfile) map[string]any {
	return map[string]any{
		"name": p.Name, "company": p.Company, "address": p.Address, "city": p.City,
		"postal_code": p.PostalCode, "country": p.Country, "vat_id": p.VATID,
	}
}

func (a *API) handleSaveBillingSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled        bool    `json:"enabled"`
		Currency       string  `json:"currency"`
		VATRate        int64   `json:"vat_rate"`
		InvoicePrefix  string  `json:"invoice_prefix"`
		SellerName     string  `json:"seller_name"`
		SellerAddress  string  `json:"seller_address"`
		SellerCountry  string  `json:"seller_country"`
		SellerVATID    string  `json:"seller_vat_id"`
		SellerEmail    string  `json:"seller_email"`
		StripeEnabled  bool    `json:"stripe_enabled"`
		StripeSecret   *string `json:"stripe_secret"`
		StripeWebhook  *string `json:"stripe_webhook_secret"`
		StripePublish  *string `json:"stripe_publishable"`
		RevolutEnabled bool    `json:"revolut_enabled"`
		RevolutSecret  *string `json:"revolut_secret"`
		RevolutWebhook *string `json:"revolut_webhook_secret"`
		RevolutSandbox bool    `json:"revolut_sandbox"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	if body.VATRate < 0 || body.VATRate > 10000 {
		writeError(w, http.StatusUnprocessableEntity, "VAT rate must be between 0 and 10000 basis points (0–100%).")
		return
	}
	if body.Enabled && body.SellerName == "" {
		writeError(w, http.StatusUnprocessableEntity, "A seller name is required for compliant invoices.")
		return
	}
	set := a.Store.SetSetting
	set("billing:enabled", boolSetting(body.Enabled))
	set("billing:currency", orDefault(strings.ToUpper(body.Currency), "EUR"))
	set("billing:vat_rate", strconv.FormatInt(body.VATRate, 10))
	set("billing:invoice_prefix", orDefault(strings.TrimSpace(body.InvoicePrefix), "INV"))
	set("billing:seller_name", body.SellerName)
	set("billing:seller_address", body.SellerAddress)
	set("billing:seller_country", strings.ToUpper(strings.TrimSpace(body.SellerCountry)))
	set("billing:seller_vat_id", strings.TrimSpace(body.SellerVATID))
	set("billing:seller_email", body.SellerEmail)
	set("billing:stripe_enabled", boolSetting(body.StripeEnabled))
	set("billing:revolut_enabled", boolSetting(body.RevolutEnabled))
	set("billing:revolut_sandbox", boolSetting(body.RevolutSandbox))
	// Secrets: only overwrite when a value is actually provided (nil = keep).
	setSecret := func(key string, v *string) {
		if v != nil {
			set(key, strings.TrimSpace(*v))
		}
	}
	setSecret("billing:stripe_secret", body.StripeSecret)
	setSecret("billing:stripe_webhook_secret", body.StripeWebhook)
	setSecret("billing:stripe_publishable", body.StripePublish)
	setSecret("billing:revolut_secret", body.RevolutSecret)
	setSecret("billing:revolut_webhook_secret", body.RevolutWebhook)

	a.activity(r, "admin:billing.settings", map[string]any{"enabled": body.Enabled})
	writeJSON(w, http.StatusOK, a.billingSettings())
}

func (a *API) upsertProduct(w http.ResponseWriter, r *http.Request, p *store.Product) {
	var body struct {
		Name            string `json:"name"`
		Description     string `json:"description"`
		PriceCents      int64  `json:"price_cents"`
		Currency        string `json:"currency"`
		BillingInterval string `json:"billing_interval"`
		EggID           int64  `json:"egg_id"`
		NodeID          *int64 `json:"node_id"`
		DockerImage     string `json:"docker_image"`
		Memory          int64  `json:"memory"`
		Swap            int64  `json:"swap"`
		Disk            int64  `json:"disk"`
		IO              int64  `json:"io"`
		CPU             int64  `json:"cpu"`
		Databases       int64  `json:"databases"`
		Allocations     int64  `json:"allocations"`
		Backups         int64  `json:"backups"`
		Active          *bool  `json:"active"`
		Sort            int64  `json:"sort"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	isNew := p == nil
	if isNew {
		if body.Name == "" || body.EggID == 0 || body.PriceCents < 0 {
			writeError(w, http.StatusUnprocessableEntity, "A name, egg and non-negative price are required.")
			return
		}
		if _, err := a.Store.EggByID(body.EggID); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "The requested egg does not exist.")
			return
		}
		p = &store.Product{Active: true, Currency: "EUR", BillingInterval: "month",
			IO: 500, Allocations: 1}
	}
	switch body.BillingInterval {
	case "", "month", "year", "one_time":
	default:
		writeError(w, http.StatusUnprocessableEntity, "billing_interval must be one_time, month or year.")
		return
	}
	if body.Name != "" {
		p.Name = body.Name
	}
	p.Description = body.Description
	if body.PriceCents >= 0 {
		p.PriceCents = body.PriceCents
	}
	if body.Currency != "" {
		p.Currency = strings.ToUpper(body.Currency)
	}
	if body.BillingInterval != "" {
		p.BillingInterval = body.BillingInterval
	}
	if body.EggID != 0 {
		p.EggID = body.EggID
	}
	p.NodeID = body.NodeID
	p.DockerImage = body.DockerImage
	p.Memory, p.Swap, p.Disk = body.Memory, body.Swap, body.Disk
	if body.IO > 0 {
		p.IO = body.IO
	}
	p.CPU, p.Databases, p.Allocations, p.Backups = body.CPU, body.Databases, body.Allocations, body.Backups
	if body.Allocations == 0 {
		p.Allocations = 1
	}
	p.Sort = body.Sort
	if body.Active != nil {
		p.Active = *body.Active
	}

	var err error
	if isNew {
		err = a.Store.CreateProduct(p)
	} else {
		err = a.Store.UpdateProduct(p)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeItem(w, http.StatusOK, "product", trProduct(p))
}

// ---- checkout ----

func (a *API) handleCheckout(w http.ResponseWriter, r *http.Request) {
	cfg := a.billingSettings()
	if !cfg.Enabled {
		writeError(w, http.StatusBadRequest, "Billing is not enabled on this panel.")
		return
	}
	u := userFrom(r)
	var body struct {
		ProductID int64  `json:"product_id"`
		Provider  string `json:"provider"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	product, err := a.Store.ProductByID(body.ProductID)
	if err != nil || !product.Active {
		writeError(w, http.StatusNotFound, "That product is not available.")
		return
	}
	prov := a.provider(body.Provider)
	if prov == nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("The %q payment provider is not enabled.", body.Provider))
		return
	}

	// VAT from the customer's billing profile (reverse charge for EU B2B).
	profile, _ := a.Store.BillingProfile(u.ID)
	buyerCountry, buyerVAT := "", ""
	if profile != nil {
		buyerCountry, buyerVAT = profile.Country, profile.VATID
	}
	vat := billing.ComputeVAT(product.PriceCents, cfg.VATRate, cfg.SellerCountry, buyerCountry, buyerVAT)

	order := &store.Order{
		UUID: auth.UUID(), UserID: u.ID, ProductID: product.ID, Provider: prov.Name(),
		Status: "pending", NetCents: vat.NetCents, VATCents: vat.VATCents, GrossCents: vat.GrossCents,
		VATRate: vat.RateBP, ReverseCharge: vat.ReverseCharge, Currency: product.Currency,
	}
	if err := a.Store.CreateOrder(order); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	base := a.PanelURL()
	recurring := product.BillingInterval == "month" || product.BillingInterval == "year"
	result, err := prov.CreateCheckout(billing.CheckoutRequest{
		OrderUUID:   order.UUID,
		Description: product.Name,
		AmountCents: vat.GrossCents,
		Currency:    product.Currency,
		Recurring:   recurring,
		Interval:    product.BillingInterval,
		Email:       u.Email,
		SuccessURL:  base + "/#/billing/orders?order=" + order.UUID,
		CancelURL:   base + "/#/billing/shop",
	})
	if err != nil {
		order.Status = "failed"
		a.Store.UpdateOrder(order)
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	order.ProviderRef = result.ProviderRef
	a.Store.UpdateOrder(order)

	a.activity(r, "billing:checkout.start", map[string]any{"product": product.Name, "provider": prov.Name()})
	writeJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{"redirect_url": result.RedirectURL, "order": order.UUID},
	})
}

// ---- webhooks + fulfilment ----

func (a *API) webhookHandler(providerName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		prov := a.provider(providerName)
		if prov == nil {
			writeError(w, http.StatusServiceUnavailable, "provider not configured")
			return
		}
		body := make([]byte, 0)
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			body = append(body, buf[:n]...)
			if err != nil || len(body) > 1<<20 {
				break
			}
		}
		event, err := prov.VerifyWebhook(body, r.Header)
		if err != nil {
			// Do not leak why; just refuse.
			writeError(w, http.StatusBadRequest, "invalid webhook signature")
			return
		}
		a.handleWebhookEvent(providerName, event)
		writeNoContent(w)
	}
}

func (a *API) handleWebhookEvent(providerName string, ev billing.WebhookEvent) {
	switch ev.Type {
	case billing.EventPaymentSucceeded:
		a.fulfilOrder(providerName, ev)
	case billing.EventPaymentFailed:
		if o, err := a.Store.OrderByProviderRef(providerName, ev.OrderRef); err == nil && o.Status == "pending" {
			o.Status = "failed"
			a.Store.UpdateOrder(o)
		}
	case billing.EventRefunded:
		a.refundOrder(providerName, ev.OrderRef)
	case billing.EventSubscriptionEnd:
		a.endSubscription(providerName, ev.SubscriptionID)
	case billing.EventSubscriptionPaid:
		a.renewSubscription(providerName, ev)
	}
}

// fulfilOrder marks an order paid, provisions its server, and issues an
// invoice — all idempotently, so a redelivered webhook is a no-op.
func (a *API) fulfilOrder(providerName string, ev billing.WebhookEvent) {
	order, err := a.Store.OrderByProviderRef(providerName, ev.OrderRef)
	if err != nil {
		return
	}
	if order.Status == "paid" {
		return // already fulfilled
	}
	product, err := a.Store.ProductByID(order.ProductID)
	if err != nil {
		return
	}

	// Recurring? create a subscription record first.
	if ev.SubscriptionID != "" {
		if _, err := a.Store.SubscriptionByProviderRef(providerName, ev.SubscriptionID); err != nil {
			sub := &store.Subscription{
				UUID: auth.UUID(), UserID: order.UserID, ProductID: order.ProductID,
				Provider: providerName, ProviderRef: ev.SubscriptionID, Status: "active",
				CurrentPeriodEnd: nilString(ev.PeriodEnd),
			}
			a.Store.CreateSubscription(sub)
			order.SubscriptionID = &sub.ID
		}
	}

	nodeID := int64(0)
	if product.NodeID != nil {
		nodeID = *product.NodeID
	}
	srv, err := a.provisionServer(ProvisionSpec{
		Name:        product.Name,
		OwnerID:     order.UserID,
		EggID:       product.EggID,
		NodeID:      nodeID,
		DockerImage: product.DockerImage,
		OOMDisabled: true,
		Memory:      product.Memory, Swap: product.Swap, Disk: product.Disk,
		IO: product.IO, CPU: product.CPU,
		Databases: product.Databases, Allocations: product.Allocations, Backups: product.Backups,
	})
	if err == nil {
		order.ServerID = &srv.ID
		if order.SubscriptionID != nil {
			if sub, err := a.Store.SubscriptionByID(*order.SubscriptionID); err == nil {
				sub.ServerID = &srv.ID
				a.Store.UpdateSubscription(sub)
			}
		}
	}
	// The payment succeeded regardless of provisioning; mark it paid and let an
	// admin resolve a provisioning failure (e.g. no free allocation).
	ts := nowISO()
	order.Status = "paid"
	order.PaidAt = &ts
	a.Store.UpdateOrder(order)

	a.issueInvoice(order, product)

	log := &store.ActivityLog{Event: "billing:order.paid", IP: "", Properties: fmt.Sprintf(`{"order":%q,"provisioned":%t}`, order.UUID, err == nil)}
	log.ActorID = &order.UserID
	a.Store.LogActivity(log)
}

func (a *API) issueInvoice(order *store.Order, product *store.Product) {
	if _, err := a.Store.InvoiceByOrder(order.ID); err == nil {
		return // idempotent
	}
	cfg := a.billingSettings()
	seller := map[string]any{
		"name": cfg.SellerName, "address": cfg.SellerAddress,
		"country": cfg.SellerCountry, "vat_id": cfg.SellerVATID, "email": cfg.SellerEmail,
	}
	buyer := map[string]any{}
	if p, err := a.Store.BillingProfile(order.UserID); err == nil {
		buyer = trProfile(p)
	} else if u, err := a.Store.UserByID(order.UserID); err == nil {
		buyer = map[string]any{"name": strings.TrimSpace(u.NameFirst + " " + u.NameLast), "email": u.Email}
	}
	lines := []map[string]any{{
		"description": product.Name,
		"quantity":    1,
		"unit_cents":  order.NetCents,
		"net_cents":   order.NetCents,
	}}
	sellerJSON, _ := json.Marshal(seller)
	buyerJSON, _ := json.Marshal(buyer)
	linesJSON, _ := json.Marshal(lines)

	notes := ""
	if order.ReverseCharge {
		notes = "Reverse charge — VAT to be accounted for by the recipient (Art. 196 EU VAT Directive 2006/112/EC)."
	}
	due := time.Now().UTC().Add(14 * 24 * time.Hour).Format(time.RFC3339)
	ts := nowISO()
	inv := &store.Invoice{
		UserID: order.UserID, OrderID: &order.ID, Status: "paid", Currency: order.Currency,
		NetCents: order.NetCents, VATCents: order.VATCents, GrossCents: order.GrossCents,
		VATRate: order.VATRate, ReverseCharge: order.ReverseCharge,
		Seller: string(sellerJSON), Buyer: string(buyerJSON), Lines: string(linesJSON),
		Notes: notes, IssuedAt: ts, DueAt: &due, PaidAt: &ts,
	}
	a.Store.CreateInvoice(inv, cfg.InvoicePrefix)
}

func (a *API) refundOrder(providerName, ref string) {
	order, err := a.Store.OrderByProviderRef(providerName, ref)
	if err != nil {
		return
	}
	order.Status = "refunded"
	a.Store.UpdateOrder(order)
	if order.ServerID != nil {
		a.suspendServer(*order.ServerID, "refunded")
	}
	if inv, err := a.Store.InvoiceByOrder(order.ID); err == nil {
		a.Store.MarkInvoicePaid(inv.ID, "") // status handled separately; leave paid_at
	}
}

func (a *API) endSubscription(providerName, subRef string) {
	sub, err := a.Store.SubscriptionByProviderRef(providerName, subRef)
	if err != nil {
		return
	}
	sub.Status = "canceled"
	a.Store.UpdateSubscription(sub)
	if sub.ServerID != nil {
		a.suspendServer(*sub.ServerID, "subscription-ended")
	}
}

func (a *API) renewSubscription(providerName string, ev billing.WebhookEvent) {
	sub, err := a.Store.SubscriptionByProviderRef(providerName, ev.SubscriptionID)
	if err != nil {
		return
	}
	sub.Status = "active"
	if ev.PeriodEnd != "" {
		sub.CurrentPeriodEnd = &ev.PeriodEnd
	}
	a.Store.UpdateSubscription(sub)
	// A previously suspended server (missed payment) comes back online.
	if sub.ServerID != nil {
		if srv, err := a.Store.ServerByID(*sub.ServerID); err == nil && srv.Status != nil && *srv.Status == "suspended" {
			srv.Status = nil
			a.Store.UpdateServer(srv)
			a.syncWings(srv)
		}
	}
}

func (a *API) suspendServer(serverID int64, reason string) {
	srv, err := a.Store.ServerByID(serverID)
	if err != nil {
		return
	}
	status := "suspended"
	srv.Status = &status
	a.Store.UpdateServer(srv)
	if node, err := a.Store.NodeByID(srv.NodeID); err == nil {
		go wings.New(node).Sync(srv.UUID)
	}
}

// ---- helpers ----

func nilString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func orDefault(v, d string) string {
	if strings.TrimSpace(v) == "" {
		return d
	}
	return v
}
