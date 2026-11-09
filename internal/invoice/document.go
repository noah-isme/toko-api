// Package invoice renders tenant-scoped PDF invoices for paid orders and
// assigns stable per-tenant invoice numbers.
package invoice

import "time"

// StoreInfo is the seller block printed at the top of the invoice.
type StoreInfo struct {
	Name        string
	Email       string
	Phone       string
	Description string
}

// Customer is the billed customer.
type Customer struct {
	Name  string
	Email string
}

// Address is the shipping address snapshot stored on the order.
type Address struct {
	ReceiverName string `json:"receiverName"`
	Phone        string `json:"phone"`
	Line1        string `json:"addressLine1"`
	Line2        string `json:"addressLine2"`
	City         string `json:"city"`
	Province     string `json:"province"`
	PostalCode   string `json:"postalCode"`
	Country      string `json:"country"`
}

// Item is one invoice line. Amounts are whole currency units.
type Item struct {
	Title     string
	SKU       string
	Qty       int32
	UnitPrice int64
	Subtotal  int64
}

// Document is everything the renderer prints. All amounts are whole currency
// units copied verbatim from the order (no scaling).
type Document struct {
	Number         string
	OrderNumber    string
	IssuedAt       time.Time
	OrderedAt      time.Time
	Location       *time.Location
	Store          StoreInfo
	Customer       Customer
	Address        Address
	Items          []Item
	Currency       string
	Subtotal       int64
	Discount       int64
	Tax            int64
	Shipping       int64
	Total          int64
	VoucherCode    string
	PaymentStatus  string
	PaymentChannel string
	OrderStatus    string
}
