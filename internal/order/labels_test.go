package order

import "testing"

func TestPaymentStatusLabel(t *testing.T) {
	cases := map[string]string{
		"PAID":               "paid",
		"PARTIALLY_REFUNDED": "paid",
		"PENDING":            "pending",
		"FAILED":             "failed",
		"EXPIRED":            "failed",
		"REFUNDED":           "failed",
		"":                   "pending",
	}
	for status, want := range cases {
		if got := paymentStatusLabel(status); got != want {
			t.Errorf("paymentStatusLabel(%q) = %q, want %q", status, got, want)
		}
	}
}
