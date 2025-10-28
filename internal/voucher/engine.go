package voucher

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrNotEligible is returned when the voucher cannot be applied to the provided context.
	ErrNotEligible = errors.New("voucher not eligible")
	// ErrUsageLimitReached indicates the voucher has exhausted the global usage quota.
	ErrUsageLimitReached = errors.New("voucher usage limit reached")
	// ErrPerUserLimitReached indicates the caller has exceeded the per-user allowance.
	ErrPerUserLimitReached = errors.New("voucher per-user usage limit reached")
	// ErrVoucherInactive is returned when attempting to use a voucher outside of its active window.
	ErrVoucherInactive = errors.New("voucher not active")
	// ErrVoucherExpired is returned when the voucher has already expired.
	ErrVoucherExpired = errors.New("voucher expired")
	// ErrMinimumSpendUnmet indicates the order total did not meet the voucher requirement.
	ErrMinimumSpendUnmet = errors.New("voucher minimum spend not met")
)

// Rule captures the runtime constraints of a voucher.
type Rule struct {
	Code           string
	Kind           string
	Value          int64
	PercentBps     *int32
	MinSpend       int64
	UsageLimit     *int32
	UsedCount      int32
	PerUserLimit   *int32
	ValidFrom      *time.Time
	ValidTo        *time.Time
	ProductIDs     []uuid.UUID
	CategoryIDs    []uuid.UUID
	BrandIDs       []uuid.UUID
	Combinable     bool
	Priority       int
	MaxStack       int
	DefaultLimit   int
	PerUserUsed    int32
	EffectiveLimit int32
}

// Item represents a line eligible for voucher calculation.
type Item struct {
	ProductID  *uuid.UUID
	CategoryID *uuid.UUID
	BrandID    *uuid.UUID
	Subtotal   int64
}

// Validate ensures the rule can be applied at the provided instant and order total.
func (r Rule) Validate(now time.Time, cartTotal int64) error {
	if cartTotal < r.MinSpend {
		return ErrMinimumSpendUnmet
	}
	if r.ValidFrom != nil && now.Before(*r.ValidFrom) {
		return ErrVoucherInactive
	}
	if r.ValidTo != nil && now.After(*r.ValidTo) {
		return ErrVoucherExpired
	}
	if r.UsageLimit != nil && *r.UsageLimit >= 0 && r.UsedCount >= *r.UsageLimit {
		return ErrUsageLimitReached
	}
	if r.EffectiveLimit > 0 && r.PerUserUsed >= r.EffectiveLimit {
		return ErrPerUserLimitReached
	}
	return nil
}

// EligibleSubtotal calculates the portion of the cart total that is affected by the voucher rule.
func EligibleSubtotal(items []Item, r Rule) int64 {
	var total int64
	scoped := len(r.ProductIDs) > 0 || len(r.CategoryIDs) > 0 || len(r.BrandIDs) > 0
	for _, it := range items {
		if it.Subtotal <= 0 {
			continue
		}
		if !scoped || ruleMatchesItem(r, it) {
			total += it.Subtotal
		}
	}
	return total
}

func ruleMatchesItem(r Rule, it Item) bool {
	if len(r.ProductIDs) > 0 {
		if it.ProductID == nil {
			return false
		}
		for _, id := range r.ProductIDs {
			if it.ProductID != nil && id == *it.ProductID {
				return true
			}
		}
	}
	if len(r.CategoryIDs) > 0 {
		if it.CategoryID == nil {
			return false
		}
		for _, id := range r.CategoryIDs {
			if it.CategoryID != nil && id == *it.CategoryID {
				return true
			}
		}
	}
	if len(r.BrandIDs) > 0 {
		if it.BrandID == nil {
			return false
		}
		for _, id := range r.BrandIDs {
			if it.BrandID != nil && id == *it.BrandID {
				return true
			}
		}
	}
	// If specific scopes exist but none matched explicitly, the item is not applicable.
	return len(r.ProductIDs) == 0 && len(r.CategoryIDs) == 0 && len(r.BrandIDs) == 0
}

// Compute determines the discount amount based on the rule and eligible subtotal.
func Compute(eligible int64, r Rule) int64 {
	if eligible <= 0 {
		return 0
	}
	discount := r.Value
	if strings.EqualFold(r.Kind, "percent") {
		if r.PercentBps == nil || *r.PercentBps <= 0 {
			return 0
		}
		discount = (eligible * int64(*r.PercentBps)) / 10000
	}
	if discount > eligible {
		discount = eligible
	}
	if discount < 0 {
		return 0
	}
	return discount
}
