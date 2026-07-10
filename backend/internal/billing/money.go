// Package billing implements the panel's payment layer: VAT calculation to EU
// rules, gapless invoice construction, and hosted-checkout integrations for
// Stripe and Revolut Business with webhook signature verification.
package billing

import (
	"fmt"
	"strings"
)

// VATResult breaks a gross-inclusive or net-exclusive price into its parts.
type VATResult struct {
	NetCents      int64
	VATCents      int64
	GrossCents    int64
	RateBP        int64 // basis points, e.g. 1900 = 19%
	ReverseCharge bool
}

// euVATCountries are the EU member-state codes where reverse charge applies to
// a cross-border B2B sale carrying a valid VAT id.
var euVATCountries = map[string]bool{
	"AT": true, "BE": true, "BG": true, "HR": true, "CY": true, "CZ": true, "DK": true,
	"EE": true, "FI": true, "FR": true, "DE": true, "GR": true, "HU": true, "IE": true,
	"IT": true, "LV": true, "LT": true, "LU": true, "MT": true, "NL": true, "PL": true,
	"PT": true, "RO": true, "SK": true, "SI": true, "ES": true, "SE": true,
}

func IsEUCountry(code string) bool {
	return euVATCountries[strings.ToUpper(strings.TrimSpace(code))]
}

// ComputeVAT applies the seller's VAT rate to a net price, deciding reverse
// charge from the buyer's country and VAT id.
//
// Rules implemented (the common case for a small EU digital seller):
//   - buyer in the seller's own country: charge VAT.
//   - buyer is an EU business (different country + VAT id): reverse charge, 0%.
//   - buyer elsewhere in the EU without a VAT id: charge the seller's rate
//     (simplification; a full OSS setup would use the buyer's national rate).
//   - buyer outside the EU: no EU VAT.
func ComputeVAT(netCents, rateBP int64, sellerCountry, buyerCountry, buyerVATID string) VATResult {
	sellerCountry = strings.ToUpper(strings.TrimSpace(sellerCountry))
	buyerCountry = strings.ToUpper(strings.TrimSpace(buyerCountry))
	hasVATID := strings.TrimSpace(buyerVATID) != ""

	res := VATResult{NetCents: netCents, RateBP: rateBP}

	switch {
	case sellerCountry == "" || buyerCountry == "":
		// Not enough information — fall back to charging the configured rate.
	case buyerCountry == sellerCountry:
		// Domestic sale: always charge VAT.
	case IsEUCountry(sellerCountry) && IsEUCountry(buyerCountry) && hasVATID:
		res.ReverseCharge = true
		res.RateBP = 0
	case !IsEUCountry(buyerCountry):
		// Export outside the EU: no EU VAT.
		res.RateBP = 0
	}

	res.VATCents = roundHalfUp(netCents*res.RateBP, 10000)
	res.GrossCents = netCents + res.VATCents
	return res
}

// roundHalfUp computes round(num/den) for non-negative integers.
func roundHalfUp(num, den int64) int64 {
	if den == 0 {
		return 0
	}
	return (num + den/2) / den
}

// FormatMoney renders minor units as a human string, e.g. 1999 EUR → "€19.99".
func FormatMoney(cents int64, currency string) string {
	sym := currencySymbol(currency)
	neg := ""
	if cents < 0 {
		neg = "-"
		cents = -cents
	}
	return fmt.Sprintf("%s%s%d.%02d", neg, sym, cents/100, cents%100)
}

func currencySymbol(currency string) string {
	switch strings.ToUpper(currency) {
	case "EUR":
		return "€"
	case "USD":
		return "$"
	case "GBP":
		return "£"
	default:
		return strings.ToUpper(currency) + " "
	}
}

// ProductInterval labels a billing cadence for display.
func IntervalLabel(interval string) string {
	switch interval {
	case "month":
		return "per month"
	case "year":
		return "per year"
	default:
		return "one-time"
	}
}
