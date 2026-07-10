package store

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func mkProduct(t *testing.T, s *Store, f fixture, price int64) *Product {
	t.Helper()
	p := &Product{
		Name: "Paper Plan", PriceCents: price, Currency: "EUR", BillingInterval: "month",
		EggID: f.egg.ID, Memory: 2048, Disk: 10240, IO: 500, Allocations: 1, Active: true,
	}
	if err := s.CreateProduct(p); err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	return p
}

func TestProductCrud(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	p := mkProduct(t, s, f, 1999)

	got, err := s.ProductByID(p.ID)
	if err != nil || got.Name != "Paper Plan" || got.PriceCents != 1999 {
		t.Fatalf("ProductByID = %+v, %v", got, err)
	}

	p.PriceCents = 2999
	p.Active = false
	if err := s.UpdateProduct(p); err != nil {
		t.Fatal(err)
	}
	got, _ = s.ProductByID(p.ID)
	if got.PriceCents != 2999 || got.Active {
		t.Errorf("update not applied: %+v", got)
	}

	// active-only listing filters it out.
	if active, _ := s.Products(true); len(active) != 0 {
		t.Errorf("inactive product still listed as active")
	}
	if all, _ := s.Products(false); len(all) != 1 {
		t.Errorf("Products(false) = %d, want 1", len(all))
	}
}

func TestDeleteProductRetiresWhenSold(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	p := mkProduct(t, s, f, 1000)

	// A paid order pins the product: delete should retire, not remove.
	s.CreateOrder(&Order{UUID: "o1", UserID: f.user.ID, ProductID: p.ID, Provider: "stripe",
		Status: "paid", NetCents: 1000, GrossCents: 1190, Currency: "EUR"})
	if err := s.DeleteProduct(p.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.ProductByID(p.ID)
	if err != nil {
		t.Fatal("sold product was hard-deleted, breaking order history")
	}
	if got.Active {
		t.Error("sold product should be retired (inactive)")
	}

	// A product with no paid orders is removable.
	p2 := mkProduct(t, s, f, 500)
	if err := s.DeleteProduct(p2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProductByID(p2.ID); !errors.Is(err, ErrNotFound) {
		t.Error("unsold product was not deleted")
	}
}

func TestBillingProfileUpsert(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)

	if _, err := s.BillingProfile(f.user.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing profile err = %v", err)
	}
	p := &BillingProfile{UserID: f.user.ID, Name: "Acme", Country: "DE", VATID: "DE123"}
	if err := s.UpsertBillingProfile(p); err != nil {
		t.Fatal(err)
	}
	p.VATID = "DE999"
	if err := s.UpsertBillingProfile(p); err != nil {
		t.Fatal(err)
	}
	got, _ := s.BillingProfile(f.user.ID)
	if got.VATID != "DE999" {
		t.Errorf("upsert did not update: %+v", got)
	}
}

func TestOrderLifecycle(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	p := mkProduct(t, s, f, 1999)

	o := &Order{UUID: "order-1", UserID: f.user.ID, ProductID: p.ID, Provider: "stripe",
		ProviderRef: "cs_1", Status: "pending", NetCents: 1999, VATCents: 380,
		GrossCents: 2379, VATRate: 1900, Currency: "EUR"}
	if err := s.CreateOrder(o); err != nil {
		t.Fatal(err)
	}

	byRef, err := s.OrderByProviderRef("stripe", "cs_1")
	if err != nil || byRef.ID != o.ID {
		t.Fatalf("OrderByProviderRef: %v", err)
	}
	if _, err := s.OrderByProviderRef("revolut", "cs_1"); !errors.Is(err, ErrNotFound) {
		t.Error("provider ref is not scoped by provider")
	}

	ts := now()
	o.Status = "paid"
	o.PaidAt = &ts
	if err := s.UpdateOrder(o); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.OrderByUUID("order-1"); got.Status != "paid" || got.PaidAt == nil {
		t.Error("order not marked paid")
	}

	if orders, _ := s.OrdersForUser(f.user.ID); len(orders) != 1 {
		t.Errorf("OrdersForUser = %d, want 1", len(orders))
	}
}

func TestInvoiceNumbersAreGaplessAndUnique(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)

	numbers := map[string]bool{}
	for i := 0; i < 5; i++ {
		inv := &Invoice{UserID: f.user.ID, Status: "paid", Currency: "EUR",
			NetCents: 1000, VATCents: 190, GrossCents: 1190, VATRate: 1900,
			Seller: "{}", Buyer: "{}", Lines: "[]", IssuedAt: now()}
		if err := s.CreateInvoice(inv, "INV"); err != nil {
			t.Fatal(err)
		}
		if numbers[inv.Number] {
			t.Fatalf("duplicate invoice number %q", inv.Number)
		}
		numbers[inv.Number] = true
	}

	year := time.Now().UTC().Format("2006")
	want := []string{
		"INV-" + year + "-0001", "INV-" + year + "-0002", "INV-" + year + "-0003",
		"INV-" + year + "-0004", "INV-" + year + "-0005",
	}
	for _, w := range want {
		if !numbers[w] {
			t.Errorf("missing sequential number %q; got %v", w, numbers)
		}
	}
}

// The sequence must stay gapless under concurrent issuance.
func TestInvoiceSequenceConcurrent(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)

	const n = 20
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[string]bool{}
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			inv := &Invoice{UserID: f.user.ID, Status: "paid", Currency: "EUR",
				NetCents: 100, GrossCents: 100, Seller: "{}", Buyer: "{}", Lines: "[]", IssuedAt: now()}
			if err := s.CreateInvoice(inv, "INV"); err != nil {
				errs <- err
				return
			}
			mu.Lock()
			if seen[inv.Number] {
				errs <- errors.New("duplicate number: " + inv.Number)
			}
			seen[inv.Number] = true
			mu.Unlock()
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if len(seen) != n {
		t.Errorf("issued %d distinct numbers, want %d", len(seen), n)
	}
	// And they are exactly 1..n with no gaps.
	year := time.Now().UTC().Format("2006")
	for i := 1; i <= n; i++ {
		num := "INV-" + year + "-" + pad4(i)
		if !seen[num] {
			t.Errorf("gap in sequence: missing %q", num)
		}
	}
}

func pad4(n int) string {
	s := ""
	for _, d := range []int{1000, 100, 10, 1} {
		s += string(rune('0' + (n/d)%10))
	}
	return s
}

func TestInvoiceLookups(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	o := &Order{UUID: "o", UserID: f.user.ID, ProductID: 0, Provider: "stripe",
		Status: "paid", NetCents: 1, GrossCents: 1, Currency: "EUR"}
	// product_id 0 would violate the FK, so make a product.
	p := mkProduct(t, s, f, 1)
	o.ProductID = p.ID
	s.CreateOrder(o)

	inv := &Invoice{UserID: f.user.ID, OrderID: &o.ID, Status: "paid", Currency: "EUR",
		NetCents: 1, GrossCents: 1, Seller: "{}", Buyer: "{}", Lines: "[]", IssuedAt: now()}
	s.CreateInvoice(inv, "INV")

	if got, err := s.InvoiceByNumber(inv.Number); err != nil || got.ID != inv.ID {
		t.Errorf("InvoiceByNumber: %v", err)
	}
	if got, err := s.InvoiceByOrder(o.ID); err != nil || got.ID != inv.ID {
		t.Errorf("InvoiceByOrder: %v", err)
	}
	if list, _ := s.InvoicesForUser(f.user.ID); len(list) != 1 {
		t.Errorf("InvoicesForUser = %d, want 1", len(list))
	}
	if !strings.HasPrefix(inv.Number, "INV-") {
		t.Errorf("number = %q", inv.Number)
	}
}

func TestSubscriptionLifecycle(t *testing.T) {
	s := newStore(t)
	f := mkFixture(t, s)
	p := mkProduct(t, s, f, 500)

	sub := &Subscription{UUID: "sub-1", UserID: f.user.ID, ProductID: p.ID, Provider: "stripe",
		ProviderRef: "sub_stripe_1", Status: "active"}
	if err := s.CreateSubscription(sub); err != nil {
		t.Fatal(err)
	}
	got, err := s.SubscriptionByProviderRef("stripe", "sub_stripe_1")
	if err != nil || got.ID != sub.ID {
		t.Fatalf("SubscriptionByProviderRef: %v", err)
	}

	end := time.Now().Add(30 * 24 * time.Hour).Format(time.RFC3339)
	sub.CurrentPeriodEnd = &end
	sub.Status = "canceled"
	if err := s.UpdateSubscription(sub); err != nil {
		t.Fatal(err)
	}
	got, _ = s.SubscriptionByID(sub.ID)
	if got.Status != "canceled" || got.CurrentPeriodEnd == nil {
		t.Errorf("subscription update not applied: %+v", got)
	}
	if subs, _ := s.SubscriptionsForUser(f.user.ID); len(subs) != 1 {
		t.Errorf("SubscriptionsForUser = %d, want 1", len(subs))
	}
}
