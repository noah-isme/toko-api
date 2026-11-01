// Package money formats monetary amounts for human-readable output (PDF
// invoices, notification copy).
//
// Amounts in this codebase are stored in WHOLE currency units (rupiah), exactly
// as in products.price and orders.pricing_*. Format never divides or scales.
package money

import (
	"strconv"
	"strings"
)

// Format renders a whole-unit amount for display.
//
//	Format("IDR", 1250000) == "Rp 1.250.000"
//	Format("IDR", -5000)   == "-Rp 5.000"
//	Format("USD", 125000)  == "USD 125,000"
//	Format("", 10)         == "10"
func Format(currency string, amount int64) string {
	negative := amount < 0
	// Work on the unsigned magnitude so math.MinInt64 does not overflow.
	magnitude := uint64(amount)
	if negative {
		magnitude = uint64(-(amount + 1)) + 1
	}
	digits := strconv.FormatUint(magnitude, 10)

	code := strings.ToUpper(strings.TrimSpace(currency))
	var out string
	switch code {
	case "IDR":
		out = "Rp " + group(digits, '.')
	case "":
		out = group(digits, ',')
	default:
		out = code + " " + group(digits, ',')
	}
	if negative {
		return "-" + out
	}
	return out
}

func group(digits string, sep byte) string {
	if len(digits) <= 3 {
		return digits
	}
	var b strings.Builder
	head := len(digits) % 3
	if head > 0 {
		b.WriteString(digits[:head])
	}
	for i := head; i < len(digits); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(sep)
		}
		b.WriteString(digits[i : i+3])
	}
	return b.String()
}
