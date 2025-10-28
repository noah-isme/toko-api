package voucher

import (
	"testing"

	"github.com/google/uuid"
)

func TestComputePercent(t *testing.T) {
	percent := int32(2000)
	rule := Rule{Kind: "percent", PercentBps: &percent}
	discount := Compute(100_000, rule)
	if discount != 20_000 {
		t.Fatalf("expected 20000 discount, got %d", discount)
	}
}

func TestEligibleSubtotalScoped(t *testing.T) {
	prodID := uuidMust("11111111-1111-1111-1111-111111111111")
	otherProd := uuidMust("22222222-2222-2222-2222-222222222222")
	rule := Rule{ProductIDs: []uuid.UUID{prodID}}
	items := []Item{
		{ProductID: &prodID, Subtotal: 50_000},
		{ProductID: &otherProd, Subtotal: 70_000},
	}
	eligible := EligibleSubtotal(items, rule)
	if eligible != 50_000 {
		t.Fatalf("expected eligible subtotal 50000, got %d", eligible)
	}
}

func uuidMust(value string) uuid.UUID {
	id, err := uuid.Parse(value)
	if err != nil {
		panic(err)
	}
	return id
}
