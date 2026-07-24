package checkout

import (
	"encoding/json"
	"testing"
	"time"
)

func TestInputMatchesContract(t *testing.T) {
	raw := `{"cartId":"cart-uuid","shippingAddressId":"address-uuid","shippingService":"jne-reg","shippingCost":15000,"paymentMethod":"bank_transfer","notes":"Please call before delivery"}`
	var in Input
	if err := json.Unmarshal([]byte(raw), &in); err != nil {
		t.Fatalf("unmarshal contract request: %v", err)
	}
	if in.CartID != "cart-uuid" {
		t.Errorf("cartId = %q, want cart-uuid", in.CartID)
	}
	if in.ShippingAddressID != "address-uuid" {
		t.Errorf("shippingAddressId = %q, want address-uuid", in.ShippingAddressID)
	}
	if in.ShippingService != "jne-reg" {
		t.Errorf("shippingService = %q, want jne-reg", in.ShippingService)
	}
	if in.ShippingCost != 15000 {
		t.Errorf("shippingCost = %d, want 15000", in.ShippingCost)
	}
	if in.PaymentMethod != "bank_transfer" {
		t.Errorf("paymentMethod = %q, want bank_transfer", in.PaymentMethod)
	}
	if in.Notes == nil || *in.Notes != "Please call before delivery" {
		t.Errorf("notes = %v, want non-nil string", in.Notes)
	}
}

func TestOutputMatchesContract(t *testing.T) {
	created := time.Date(2025, 12, 7, 10, 0, 0, 0, time.UTC)
	expiry := created.Add(24 * time.Hour)
	out := Output{
		OrderID:       "order-uuid",
		OrderNumber:   "ORD-20251207-001",
		Status:        "pending_payment",
		Total:         21135000,
		Currency:      "IDR",
		PaymentMethod: "bank_transfer",
		PaymentURL:    "https://payment.gateway.com/pay/xxx",
		PaymentExpiry: &expiry,
		CreatedAt:     created,
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	wantKeys := map[string]bool{
		"orderId": true, "orderNumber": true, "status": true, "total": true,
		"currency": true, "paymentMethod": true, "paymentUrl": true,
		"paymentExpiry": true, "createdAt": true,
	}
	if len(m) != len(wantKeys) {
		t.Errorf("output has %d keys, want %d (%v)", len(m), len(wantKeys), m)
	}
	for k := range wantKeys {
		if _, ok := m[k]; !ok {
			t.Errorf("missing contract key %q in output %v", k, m)
		}
	}
	if m["status"] != "pending_payment" {
		t.Errorf("status = %v, want pending_payment (lowercase)", m["status"])
	}
	if m["paymentUrl"] != "https://payment.gateway.com/pay/xxx" {
		t.Errorf("paymentUrl = %v, want the gateway url", m["paymentUrl"])
	}
	if m["paymentExpiry"] != "2025-12-08T10:00:00Z" {
		t.Errorf("paymentExpiry = %v, want 2025-12-08T10:00:00Z", m["paymentExpiry"])
	}
	if m["createdAt"] != "2025-12-07T10:00:00Z" {
		t.Errorf("createdAt = %v, want 2025-12-07T10:00:00Z", m["createdAt"])
	}
}

func TestOutputNullPaymentExpiry(t *testing.T) {
	out := Output{OrderID: "order-uuid", CreatedAt: time.Time{}}
	b, _ := json.Marshal(out)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if m["paymentExpiry"] != nil {
		t.Errorf("paymentExpiry = %v, want null when unset", m["paymentExpiry"])
	}
	if m["paymentUrl"] != "" {
		t.Errorf("paymentUrl = %v, want empty string when unset", m["paymentUrl"])
	}
}

func TestFormatOrderNumber(t *testing.T) {
	day := time.Date(2025, 12, 7, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		count int64
		want  string
	}{
		{0, "ORD-20251207-001"},
		{1, "ORD-20251207-002"},
		{42, "ORD-20251207-043"},
		{999, "ORD-20251207-1000"},
	}
	for _, c := range cases {
		if got := formatOrderNumber(day, c.count); got != c.want {
			t.Errorf("formatOrderNumber(%d) = %q, want %q", c.count, got, c.want)
		}
	}
}

func TestAllowedPaymentMethodsMatchContract(t *testing.T) {
	want := []string{"bank_transfer", "virtual_account", "credit_card", "ewallet_gopay", "ewallet_ovo", "ewallet_dana"}
	if len(allowedPaymentMethods) != len(want) {
		t.Errorf("allowedPaymentMethods has %d entries, want %d (%v)", len(allowedPaymentMethods), len(want), allowedPaymentMethods)
	}
	for _, m := range want {
		if _, ok := allowedPaymentMethods[m]; !ok {
			t.Errorf("contract payment method %q not in allowedPaymentMethods", m)
		}
	}
}
