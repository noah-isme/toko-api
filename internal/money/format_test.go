package money

import (
	"math"
	"testing"
)

func TestFormat(t *testing.T) {
	cases := []struct {
		currency string
		amount   int64
		want     string
	}{
		{"IDR", 1250000, "Rp 1.250.000"},
		{"IDR", 249000, "Rp 249.000"},
		{"IDR", 999000, "Rp 999.000"},
		{"IDR", 0, "Rp 0"},
		{"IDR", 999, "Rp 999"},
		{"IDR", 1000, "Rp 1.000"},
		{"IDR", -5000, "-Rp 5.000"},
		{"idr", 25000000, "Rp 25.000.000"},
		{"USD", 125000, "USD 125,000"},
		{"", 10, "10"},
		{"", 1234567, "1,234,567"},
		{"IDR", math.MinInt64, "-Rp 9.223.372.036.854.775.808"},
	}
	for _, tc := range cases {
		if got := Format(tc.currency, tc.amount); got != tc.want {
			t.Errorf("Format(%q, %d) = %q, want %q", tc.currency, tc.amount, got, tc.want)
		}
	}
}
