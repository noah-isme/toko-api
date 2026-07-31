package order

import (
	"testing"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// The allowed set must stay in lockstep with the guard inside the
// UpdateOrderStatusIfAllowed query, so assert every pair explicitly rather than
// deriving expectations from the same map the code under test uses.
func TestIsAllowedTransition(t *testing.T) {
	all := []dbgen.OrderStatus{
		dbgen.OrderStatusPENDINGPAYMENT,
		dbgen.OrderStatusPAID,
		dbgen.OrderStatusPACKED,
		dbgen.OrderStatusSHIPPED,
		dbgen.OrderStatusOUTFORDELIVERY,
		dbgen.OrderStatusDELIVERED,
		dbgen.OrderStatusCANCELLED,
	}

	allowed := map[dbgen.OrderStatus]map[dbgen.OrderStatus]bool{
		dbgen.OrderStatusPENDINGPAYMENT: {
			dbgen.OrderStatusPAID:      true,
			dbgen.OrderStatusCANCELLED: true,
		},
		dbgen.OrderStatusPAID: {
			dbgen.OrderStatusPACKED:    true,
			dbgen.OrderStatusCANCELLED: true,
		},
		dbgen.OrderStatusPACKED:         {dbgen.OrderStatusSHIPPED: true},
		dbgen.OrderStatusSHIPPED:        {dbgen.OrderStatusOUTFORDELIVERY: true},
		dbgen.OrderStatusOUTFORDELIVERY: {dbgen.OrderStatusDELIVERED: true},
		dbgen.OrderStatusDELIVERED:      {},
		dbgen.OrderStatusCANCELLED:      {},
	}

	for _, current := range all {
		for _, target := range all {
			want := allowed[current][target]
			if got := isAllowedTransition(current, target); got != want {
				t.Errorf("isAllowedTransition(%s, %s) = %v, want %v", current, target, got, want)
			}
		}
	}
}

// Cancellation is the case the previous rank-based guard rejected outright.
func TestCancelIsAllowedBeforeFulfilment(t *testing.T) {
	for _, current := range []dbgen.OrderStatus{dbgen.OrderStatusPENDINGPAYMENT, dbgen.OrderStatusPAID} {
		if !isAllowedTransition(current, dbgen.OrderStatusCANCELLED) {
			t.Errorf("cancel from %s should be allowed", current)
		}
	}
	for _, current := range []dbgen.OrderStatus{
		dbgen.OrderStatusPACKED,
		dbgen.OrderStatusSHIPPED,
		dbgen.OrderStatusOUTFORDELIVERY,
		dbgen.OrderStatusDELIVERED,
		dbgen.OrderStatusCANCELLED,
	} {
		if isAllowedTransition(current, dbgen.OrderStatusCANCELLED) {
			t.Errorf("cancel from %s should be rejected", current)
		}
	}
}

// Every admin-settable target must be reachable from at least one state,
// otherwise the UI would offer a transition the API can never accept.
func TestAdminTargetsAreReachable(t *testing.T) {
	targets := []dbgen.OrderStatus{
		dbgen.OrderStatusPACKED,
		dbgen.OrderStatusSHIPPED,
		dbgen.OrderStatusOUTFORDELIVERY,
		dbgen.OrderStatusDELIVERED,
		dbgen.OrderStatusCANCELLED,
	}
	for _, target := range targets {
		if !isAllowedAdminTarget(target) {
			t.Fatalf("%s should be an allowed admin target", target)
		}
		reachable := false
		for current := range allowedOrderTransitions {
			if isAllowedTransition(current, target) {
				reachable = true
				break
			}
		}
		if !reachable {
			t.Errorf("admin target %s is unreachable from every state", target)
		}
	}
}
