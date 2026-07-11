package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ---- models ----

type Product struct {
	ID              int64
	Name            string
	Description     string
	PriceCents      int64
	Currency        string
	BillingInterval string // one_time | month | year
	EggID           int64
	NodeID          *int64
	DockerImage     string
	Memory          int64
	Swap            int64
	Disk            int64
	IO              int64
	CPU             int64
	Databases       int64
	Allocations     int64
	Backups         int64
	Active          bool
	Sort            int64
	Configurable    bool
	PricePerGBCents int64
	MinMemory       int64
	MaxMemory       int64
	NestID          *int64
	CreatedAt       string
	UpdatedAt       string
}

type BillingProfile struct {
	UserID     int64
	Name       string
	Company    string
	Address    string
	City       string
	PostalCode string
	Country    string
	VATID      string
	UpdatedAt  string
}

type Subscription struct {
	ID               int64
	UUID             string
	UserID           int64
	ProductID        int64
	Provider         string
	ProviderRef      string
	Status           string
	ServerID         *int64
	CurrentPeriodEnd *string
	CreatedAt        string
	UpdatedAt        string
}

type Order struct {
	ID             int64
	UUID           string
	UserID         int64
	ProductID      int64
	Provider       string
	ProviderRef    string
	Status         string
	NetCents       int64
	VATCents       int64
	GrossCents     int64
	VATRate        int64
	ReverseCharge  bool
	Currency       string
	ServerID       *int64
	SubscriptionID *int64
	ConfigMemory   int64
	ConfigEgg      int64
	CreatedAt      string
	UpdatedAt      string
	PaidAt         *string
}

type Invoice struct {
	ID            int64
	Number        string
	UserID        int64
	OrderID       *int64
	Status        string
	Currency      string
	NetCents      int64
	VATCents      int64
	GrossCents    int64
	VATRate       int64
	ReverseCharge bool
	Seller        string // JSON
	Buyer         string // JSON
	Lines         string // JSON
	Notes         string
	IssuedAt      string
	DueAt         *string
	PaidAt        *string
	CreatedAt     string
}

// ---- products ----

const productCols = `id, name, description, price_cents, currency, billing_interval, egg_id,
	node_id, docker_image, memory, swap, disk, io, cpu, databases, allocations, backups,
	active, sort, configurable, price_per_gb_cents, min_memory, max_memory, nest_id,
	created_at, updated_at`

func scanProduct(row interface{ Scan(...any) error }) (*Product, error) {
	p := &Product{}
	err := row.Scan(&p.ID, &p.Name, &p.Description, &p.PriceCents, &p.Currency, &p.BillingInterval,
		&p.EggID, &p.NodeID, &p.DockerImage, &p.Memory, &p.Swap, &p.Disk, &p.IO, &p.CPU,
		&p.Databases, &p.Allocations, &p.Backups, &p.Active, &p.Sort, &p.Configurable,
		&p.PricePerGBCents, &p.MinMemory, &p.MaxMemory, &p.NestID, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

// Products returns products, optionally only the active ones (for the shop).
func (s *Store) Products(activeOnly bool) ([]*Product, error) {
	q := `SELECT ` + productCols + ` FROM products`
	if activeOnly {
		q += ` WHERE active = 1`
	}
	q += ` ORDER BY sort, id`
	rows, err := s.db.Query(q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Product
	for rows.Next() {
		p, err := scanProduct(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) ProductByID(id int64) (*Product, error) {
	return scanProduct(s.db.QueryRow(`SELECT `+productCols+` FROM products WHERE id = ?`, id))
}

func (s *Store) CreateProduct(p *Product) error {
	ts := now()
	p.CreatedAt, p.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO products (name, description, price_cents, currency,
		billing_interval, egg_id, node_id, docker_image, memory, swap, disk, io, cpu, databases,
		allocations, backups, active, sort, configurable, price_per_gb_cents, min_memory,
		max_memory, nest_id, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.Name, p.Description, p.PriceCents, p.Currency, p.BillingInterval, p.EggID, p.NodeID,
		p.DockerImage, p.Memory, p.Swap, p.Disk, p.IO, p.CPU, p.Databases, p.Allocations,
		p.Backups, p.Active, p.Sort, p.Configurable, p.PricePerGBCents, p.MinMemory, p.MaxMemory,
		p.NestID, ts, ts)
	if err != nil {
		return err
	}
	p.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateProduct(p *Product) error {
	p.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE products SET name=?, description=?, price_cents=?, currency=?,
		billing_interval=?, egg_id=?, node_id=?, docker_image=?, memory=?, swap=?, disk=?, io=?,
		cpu=?, databases=?, allocations=?, backups=?, active=?, sort=?, configurable=?,
		price_per_gb_cents=?, min_memory=?, max_memory=?, nest_id=?, updated_at=? WHERE id=?`,
		p.Name, p.Description, p.PriceCents, p.Currency, p.BillingInterval, p.EggID, p.NodeID,
		p.DockerImage, p.Memory, p.Swap, p.Disk, p.IO, p.CPU, p.Databases, p.Allocations,
		p.Backups, p.Active, p.Sort, p.Configurable, p.PricePerGBCents, p.MinMemory, p.MaxMemory,
		p.NestID, p.UpdatedAt, p.ID)
	return err
}

func (s *Store) DeleteProduct(id int64) error {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM orders WHERE product_id = ? AND status = 'paid'`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		// Preserve history: retire rather than delete a sold product.
		_, err := s.db.Exec(`UPDATE products SET active = 0, updated_at = ? WHERE id = ?`, now(), id)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM products WHERE id = ?`, id)
	return err
}

// ---- billing profiles ----

func (s *Store) BillingProfile(userID int64) (*BillingProfile, error) {
	p := &BillingProfile{}
	err := s.db.QueryRow(`SELECT user_id, name, company, address, city, postal_code, country,
		vat_id, updated_at FROM billing_profiles WHERE user_id = ?`, userID).
		Scan(&p.UserID, &p.Name, &p.Company, &p.Address, &p.City, &p.PostalCode, &p.Country,
			&p.VATID, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return p, err
}

func (s *Store) UpsertBillingProfile(p *BillingProfile) error {
	p.UpdatedAt = now()
	_, err := s.db.Exec(`INSERT INTO billing_profiles (user_id, name, company, address, city,
		postal_code, country, vat_id, updated_at) VALUES (?,?,?,?,?,?,?,?,?)
		ON CONFLICT (user_id) DO UPDATE SET name=excluded.name, company=excluded.company,
		address=excluded.address, city=excluded.city, postal_code=excluded.postal_code,
		country=excluded.country, vat_id=excluded.vat_id, updated_at=excluded.updated_at`,
		p.UserID, p.Name, p.Company, p.Address, p.City, p.PostalCode, p.Country, p.VATID, p.UpdatedAt)
	return err
}

// ---- orders ----

const orderCols = `id, uuid, user_id, product_id, provider, provider_ref, status, net_cents,
	vat_cents, gross_cents, vat_rate, reverse_charge, currency, server_id, subscription_id,
	config_memory, config_egg, created_at, updated_at, paid_at`

func scanOrder(row interface{ Scan(...any) error }) (*Order, error) {
	o := &Order{}
	err := row.Scan(&o.ID, &o.UUID, &o.UserID, &o.ProductID, &o.Provider, &o.ProviderRef,
		&o.Status, &o.NetCents, &o.VATCents, &o.GrossCents, &o.VATRate, &o.ReverseCharge,
		&o.Currency, &o.ServerID, &o.SubscriptionID, &o.ConfigMemory, &o.ConfigEgg,
		&o.CreatedAt, &o.UpdatedAt, &o.PaidAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return o, err
}

func (s *Store) CreateOrder(o *Order) error {
	ts := now()
	o.CreatedAt, o.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO orders (uuid, user_id, product_id, provider, provider_ref,
		status, net_cents, vat_cents, gross_cents, vat_rate, reverse_charge, currency, server_id,
		subscription_id, config_memory, config_egg, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		o.UUID, o.UserID, o.ProductID, o.Provider, o.ProviderRef, o.Status, o.NetCents, o.VATCents,
		o.GrossCents, o.VATRate, o.ReverseCharge, o.Currency, o.ServerID, o.SubscriptionID,
		o.ConfigMemory, o.ConfigEgg, ts, ts)
	if err != nil {
		return err
	}
	o.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateOrder(o *Order) error {
	o.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE orders SET provider_ref=?, status=?, server_id=?, subscription_id=?,
		updated_at=?, paid_at=? WHERE id=?`,
		o.ProviderRef, o.Status, o.ServerID, o.SubscriptionID, o.UpdatedAt, o.PaidAt, o.ID)
	return err
}

func (s *Store) OrderByID(id int64) (*Order, error) {
	return scanOrder(s.db.QueryRow(`SELECT `+orderCols+` FROM orders WHERE id = ?`, id))
}

func (s *Store) OrderByUUID(uuid string) (*Order, error) {
	return scanOrder(s.db.QueryRow(`SELECT `+orderCols+` FROM orders WHERE uuid = ?`, uuid))
}

// OrderByProviderRef resolves the order a webhook refers to.
func (s *Store) OrderByProviderRef(provider, ref string) (*Order, error) {
	return scanOrder(s.db.QueryRow(`SELECT `+orderCols+` FROM orders
		WHERE provider = ? AND provider_ref = ?`, provider, ref))
}

func (s *Store) OrdersForUser(userID int64) ([]*Order, error) {
	return s.orderQuery(`SELECT `+orderCols+` FROM orders WHERE user_id = ? ORDER BY id DESC`, userID)
}

func (s *Store) Orders() ([]*Order, error) {
	return s.orderQuery(`SELECT ` + orderCols + ` FROM orders ORDER BY id DESC`)
}

func (s *Store) orderQuery(q string, args ...any) ([]*Order, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ---- subscriptions ----

const subCols = `id, uuid, user_id, product_id, provider, provider_ref, status, server_id,
	current_period_end, created_at, updated_at`

func scanSub(row interface{ Scan(...any) error }) (*Subscription, error) {
	v := &Subscription{}
	err := row.Scan(&v.ID, &v.UUID, &v.UserID, &v.ProductID, &v.Provider, &v.ProviderRef,
		&v.Status, &v.ServerID, &v.CurrentPeriodEnd, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func (s *Store) CreateSubscription(v *Subscription) error {
	ts := now()
	v.CreatedAt, v.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO subscriptions (uuid, user_id, product_id, provider,
		provider_ref, status, server_id, current_period_end, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		v.UUID, v.UserID, v.ProductID, v.Provider, v.ProviderRef, v.Status, v.ServerID,
		v.CurrentPeriodEnd, ts, ts)
	if err != nil {
		return err
	}
	v.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateSubscription(v *Subscription) error {
	v.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE subscriptions SET provider_ref=?, status=?, server_id=?,
		current_period_end=?, updated_at=? WHERE id=?`,
		v.ProviderRef, v.Status, v.ServerID, v.CurrentPeriodEnd, v.UpdatedAt, v.ID)
	return err
}

func (s *Store) SubscriptionByID(id int64) (*Subscription, error) {
	return scanSub(s.db.QueryRow(`SELECT `+subCols+` FROM subscriptions WHERE id = ?`, id))
}

func (s *Store) SubscriptionByProviderRef(provider, ref string) (*Subscription, error) {
	return scanSub(s.db.QueryRow(`SELECT `+subCols+` FROM subscriptions
		WHERE provider = ? AND provider_ref = ?`, provider, ref))
}

func (s *Store) SubscriptionsForUser(userID int64) ([]*Subscription, error) {
	rows, err := s.db.Query(`SELECT `+subCols+` FROM subscriptions WHERE user_id = ? ORDER BY id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Subscription
	for rows.Next() {
		v, err := scanSub(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ---- invoices ----

const invoiceCols = `id, number, user_id, order_id, status, currency, net_cents, vat_cents,
	gross_cents, vat_rate, reverse_charge, seller, buyer, lines, notes, issued_at, due_at,
	paid_at, created_at`

func scanInvoice(row interface{ Scan(...any) error }) (*Invoice, error) {
	v := &Invoice{}
	err := row.Scan(&v.ID, &v.Number, &v.UserID, &v.OrderID, &v.Status, &v.Currency, &v.NetCents,
		&v.VATCents, &v.GrossCents, &v.VATRate, &v.ReverseCharge, &v.Seller, &v.Buyer, &v.Lines,
		&v.Notes, &v.IssuedAt, &v.DueAt, &v.PaidAt, &v.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

// CreateInvoice issues an invoice with a gapless, per-year sequential number,
// allocated atomically so concurrent issuance can never collide or skip.
// The prefix is typically "INV"; the number reads e.g. INV-2026-0001.
func (s *Store) CreateInvoice(v *Invoice, prefix string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	year := time.Now().UTC().Format("2006")
	var last int64
	err = tx.QueryRow(`SELECT last FROM invoice_sequences WHERE year = ?`, year).Scan(&last)
	if errors.Is(err, sql.ErrNoRows) {
		last = 0
	} else if err != nil {
		return err
	}
	next := last + 1
	if _, err := tx.Exec(`INSERT INTO invoice_sequences (year, last) VALUES (?, ?)
		ON CONFLICT (year) DO UPDATE SET last = excluded.last`, year, next); err != nil {
		return err
	}

	v.Number = fmt.Sprintf("%s-%s-%04d", prefix, year, next)
	v.CreatedAt = now()
	res, err := tx.Exec(`INSERT INTO invoices (number, user_id, order_id, status, currency,
		net_cents, vat_cents, gross_cents, vat_rate, reverse_charge, seller, buyer, lines, notes,
		issued_at, due_at, paid_at, created_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.Number, v.UserID, v.OrderID, v.Status, v.Currency, v.NetCents, v.VATCents, v.GrossCents,
		v.VATRate, v.ReverseCharge, v.Seller, v.Buyer, v.Lines, v.Notes, v.IssuedAt, v.DueAt,
		v.PaidAt, v.CreatedAt)
	if err != nil {
		return err
	}
	v.ID, _ = res.LastInsertId()
	return tx.Commit()
}

func (s *Store) MarkInvoicePaid(id int64, paidAt string) error {
	_, err := s.db.Exec(`UPDATE invoices SET status = 'paid', paid_at = ? WHERE id = ?`, paidAt, id)
	return err
}

func (s *Store) InvoiceByID(id int64) (*Invoice, error) {
	return scanInvoice(s.db.QueryRow(`SELECT `+invoiceCols+` FROM invoices WHERE id = ?`, id))
}

func (s *Store) InvoiceByNumber(number string) (*Invoice, error) {
	return scanInvoice(s.db.QueryRow(`SELECT `+invoiceCols+` FROM invoices WHERE number = ?`, number))
}

func (s *Store) InvoiceByOrder(orderID int64) (*Invoice, error) {
	return scanInvoice(s.db.QueryRow(`SELECT `+invoiceCols+` FROM invoices WHERE order_id = ?`, orderID))
}

func (s *Store) InvoicesForUser(userID int64) ([]*Invoice, error) {
	return s.invoiceQuery(`SELECT `+invoiceCols+` FROM invoices WHERE user_id = ? ORDER BY id DESC`, userID)
}

func (s *Store) Invoices() ([]*Invoice, error) {
	return s.invoiceQuery(`SELECT ` + invoiceCols + ` FROM invoices ORDER BY id DESC`)
}

func (s *Store) invoiceQuery(q string, args ...any) ([]*Invoice, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Invoice
	for rows.Next() {
		v, err := scanInvoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
