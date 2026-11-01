package payment

import (
	"testing"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

func TestNormaliseWebhookStatusRefunds(t *testing.T) {
	cases := []struct {
		in   string
		want dbgen.PaymentStatus
	}{
		{normaliseMidtransStatus("refund"), dbgen.PaymentStatusREFUNDED},
		{"REFUNDED", dbgen.PaymentStatusREFUNDED},
		{"PARTIAL_REFUND", dbgen.PaymentStatusPARTIALLYREFUNDED},
		{normaliseMidtransStatus("partial_refund"), dbgen.PaymentStatusPARTIALLYREFUNDED},
		{"PARTIALLY_REFUNDED", dbgen.PaymentStatusPARTIALLYREFUNDED},
		{"something-new", dbgen.PaymentStatusPENDING},
		{"SETTLED", dbgen.PaymentStatusPAID},
	}
	for _, tc := range cases {
		if got := normaliseWebhookStatus(tc.in); got != tc.want {
			t.Errorf("normaliseWebhookStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestIsRefundWebhookStatus(t *testing.T) {
	for _, s := range []dbgen.PaymentStatus{dbgen.PaymentStatusREFUNDED, dbgen.PaymentStatusPARTIALLYREFUNDED} {
		if !isRefundWebhookStatus(s) {
			t.Errorf("isRefundWebhookStatus(%q) = false, want true", s)
		}
	}
	for _, s := range []dbgen.PaymentStatus{dbgen.PaymentStatusPAID, dbgen.PaymentStatusFAILED, dbgen.PaymentStatusEXPIRED, dbgen.PaymentStatusPENDING} {
		if isRefundWebhookStatus(s) {
			t.Errorf("isRefundWebhookStatus(%q) = true, want false", s)
		}
	}
}
