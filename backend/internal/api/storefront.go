package api

import (
	"net/http"

	"roost/internal/billing"
	"roost/internal/store"
)

// The storefront is the public, unauthenticated face of the panel: a marketing
// catalogue of hosting packages and the games they run, so a visitor can browse
// and configure a plan before signing in. It exposes no secrets and no private
// data — only active products and the public egg catalogue.

func (a *API) routesStorefront(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/storefront", func(w http.ResponseWriter, r *http.Request) {
		cfg := a.billingSettings()
		out := map[string]any{
			"app_name":             a.AppName(),
			"enabled":              cfg.Enabled,
			"currency":             cfg.Currency,
			"registration_enabled": a.Store.Setting("registration:enabled", "") == "1",
			"packages":             []any{},
			"games":                []any{},
			"providers":            a.enabledProviders(),
		}
		if !cfg.Enabled {
			writeJSON(w, http.StatusOK, out)
			return
		}

		products, _ := a.Store.Products(true)
		packages := make([]map[string]any, 0, len(products))
		for _, p := range products {
			packages = append(packages, a.trStorefrontPackage(p))
		}
		out["packages"] = packages

		// Games catalogue: each nest with its playable eggs. This powers both
		// the "games we host" showcase and the configurator's game picker.
		nests, _ := a.Store.Nests()
		games := make([]map[string]any, 0, len(nests))
		for _, nest := range nests {
			eggs, _ := a.Store.EggsForNest(nest.ID)
			eggList := make([]map[string]any, 0, len(eggs))
			for _, e := range eggs {
				eggList = append(eggList, map[string]any{
					"id":          e.ID,
					"name":        e.Name,
					"description": e.Description,
					"features":    jsonArr(e.Features),
				})
			}
			games = append(games, map[string]any{
				"id":          nest.ID,
				"name":        nest.Name,
				"description": nest.Description,
				"eggs":        eggList,
			})
		}
		out["games"] = games
		writeJSON(w, http.StatusOK, out)
	})
}

// enabledProviders lists the payment methods a visitor can pay with.
func (a *API) enabledProviders() []string {
	var out []string
	if a.provider("stripe") != nil {
		out = append(out, "stripe")
	}
	if a.provider("revolut") != nil {
		out = append(out, "revolut")
	}
	if out == nil {
		out = []string{}
	}
	return out
}

// trStorefrontPackage is a product as shown on the public storefront: pricing,
// the server spec, and (for configurable plans) the RAM range + per-GB price so
// the browser can compute a live quote.
func (a *API) trStorefrontPackage(p *store.Product) map[string]any {
	out := map[string]any{
		"id":               p.ID,
		"name":             p.Name,
		"description":      p.Description,
		"price_cents":      p.PriceCents,
		"price":            billing.FormatMoney(p.PriceCents, p.Currency),
		"currency":         p.Currency,
		"billing_interval": p.BillingInterval,
		"interval_label":   billing.IntervalLabel(p.BillingInterval),
		"configurable":     p.Configurable,
		"limits": map[string]any{
			"memory": p.Memory, "disk": p.Disk, "cpu": p.CPU,
		},
		"feature_limits": map[string]any{
			"databases": p.Databases, "allocations": p.Allocations, "backups": p.Backups,
		},
	}
	if p.Configurable {
		out["min_memory"] = p.MinMemory
		out["max_memory"] = p.MaxMemory
		out["price_per_gb_cents"] = p.PricePerGBCents
		out["price_per_gb"] = billing.FormatMoney(p.PricePerGBCents, p.Currency)
		// The set of games this plan can run.
		if p.NestID != nil {
			eggs, _ := a.Store.EggsForNest(*p.NestID)
			opts := make([]map[string]any, 0, len(eggs))
			for _, e := range eggs {
				opts = append(opts, map[string]any{"id": e.ID, "name": e.Name})
			}
			out["nest_id"] = *p.NestID
			out["game_options"] = opts
		}
	} else {
		out["egg_id"] = p.EggID
	}
	return out
}

// configuredPrice computes the net price for a (possibly configured) product.
// For a configurable plan the memory is chosen by the customer and priced at
// base + per-GB. It also returns the resolved egg and memory to provision.
func (a *API) configuredPrice(p *store.Product, memory, eggID int64) (netCents, provMemory, provEgg int64, err error) {
	if !p.Configurable {
		return p.PriceCents, p.Memory, p.EggID, nil
	}
	if memory < p.MinMemory || memory > p.MaxMemory {
		return 0, 0, 0, errConfig("selected memory is outside this plan's range")
	}
	// The chosen game must belong to the plan's nest.
	egg, e := a.Store.EggByID(eggID)
	if e != nil || (p.NestID != nil && egg.NestID != *p.NestID) {
		return 0, 0, 0, errConfig("the selected game is not available for this plan")
	}
	gib := memory / 1024
	if memory%1024 != 0 {
		gib++ // round partial GiB up
	}
	net := p.PriceCents + p.PricePerGBCents*gib
	return net, memory, egg.ID, nil
}

type errConfig string

func (e errConfig) Error() string { return string(e) }
