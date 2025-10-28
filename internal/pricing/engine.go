package pricing

// Money represents a monetary value stored in minor units.
type Money = int64

// Item describes a line item used for pricing calculation.
type Item struct {
	Qty       int
	UnitPrice Money
}

// Summary aggregates computed pricing components.
type Summary struct {
	Subtotal Money
	Discount Money
	Tax      Money
	Shipping Money
	Total    Money
}

// Compute calculates cart totals given the provided inputs.
func Compute(items []Item, voucher Money, taxBps int, shipping Money) Summary {
	var subtotal Money
	for _, it := range items {
		if it.Qty <= 0 {
			continue
		}
		subtotal += Money(it.Qty) * it.UnitPrice
	}
	if voucher > subtotal {
		voucher = subtotal
	}
	taxable := subtotal - voucher
	if taxable < 0 {
		taxable = 0
	}
	tax := (taxable * Money(taxBps)) / 10000
	total := taxable + tax + shipping
	return Summary{
		Subtotal: subtotal,
		Discount: voucher,
		Tax:      tax,
		Shipping: shipping,
		Total:    total,
	}
}
