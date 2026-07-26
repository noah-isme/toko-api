package checkout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/cart"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/payment"
	"github.com/noah-isme/backend-toko/internal/pricing"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// allowedPaymentMethods mirrors the contract enum in
// docs/contracts/checkout.md.
var allowedPaymentMethods = map[string]struct{}{
	"bank_transfer":   {},
	"virtual_account": {},
	"credit_card":     {},
	"ewallet_gopay":   {},
	"ewallet_ovo":     {},
	"ewallet_dana":    {},
}

// Input is the request shape for POST /api/v1/checkout, matching the contract.
type Input struct {
	CartID            string  `json:"cartId"`
	ShippingAddressID string  `json:"shippingAddressId"`
	ShippingService   string  `json:"shippingService"`
	ShippingCost      int64   `json:"shippingCost"`
	PaymentMethod     string  `json:"paymentMethod"`
	Notes             *string `json:"notes"`
}

// Output is the response shape for POST /api/v1/checkout, matching the contract.
type Output struct {
	OrderID       string     `json:"orderId"`
	OrderNumber   string     `json:"orderNumber"`
	Status        string     `json:"status"`
	Total         int64      `json:"total"`
	Currency      string     `json:"currency"`
	PaymentMethod string     `json:"paymentMethod"`
	PaymentURL    string     `json:"paymentUrl"`
	PaymentExpiry *time.Time `json:"paymentExpiry"`
	CreatedAt     time.Time  `json:"createdAt"`
}

// DraftOutput is the checkout review payload: everything the shopper needs to
// confirm before an order exists. Field names mirror the storefront's
// OrderDraftSchema.
type DraftOutput struct {
	CartID         string              `json:"cartId"`
	Address        DraftAddress        `json:"address"`
	ShippingOption DraftShippingOption `json:"shippingOption"`
	PaymentMethod  string              `json:"paymentMethod,omitempty"`
	Notes          string              `json:"notes,omitempty"`
	Totals         DraftTotals         `json:"totals"`
}

type DraftAddress struct {
	ReceiverName string `json:"receiverName"`
	Phone        string `json:"phone"`
	AddressLine1 string `json:"addressLine1"`
	AddressLine2 string `json:"addressLine2,omitempty"`
	City         string `json:"city"`
	Province     string `json:"province"`
	PostalCode   string `json:"postalCode"`
	Country      string `json:"country"`
}

type DraftShippingOption struct {
	ID      string `json:"id"`
	Courier string `json:"courier"`
	Service string `json:"service"`
	// ETD is only known from a shipping quote, not from the checkout input, so
	// it is omitted rather than echoed back as an empty string.
	ETD  string `json:"etd,omitempty"`
	Cost int64  `json:"cost"`
}

type DraftTotals struct {
	Subtotal int64 `json:"subtotal"`
	Discount int64 `json:"discount"`
	Tax      int64 `json:"tax"`
	Shipping int64 `json:"shipping"`
	Total    int64 `json:"total"`
}

type Service struct {
	Q          *dbgen.Queries
	Pool       *pgxpool.Pool
	CartSvc    *cart.Service
	PaymentSvc *payment.Service
	TaxBps     int
	Currency   string
	Events     *events.Bus
}

func (s *Service) Create(ctx context.Context, userID *string, in Input) (Output, error) {
	if s == nil || s.Q == nil || s.Pool == nil {
		return Output{}, errors.New("checkout service not configured")
	}
	if userID == nil || *userID == "" {
		return Output{}, errors.New("user is required for checkout")
	}
	if in.CartID == "" {
		return Output{}, errors.New("cartId is required")
	}
	if in.ShippingAddressID == "" {
		return Output{}, errors.New("shippingAddressId is required")
	}
	if in.ShippingService == "" {
		return Output{}, errors.New("shippingService is required")
	}
	if in.ShippingCost < 0 {
		in.ShippingCost = 0
	}
	if _, ok := allowedPaymentMethods[in.PaymentMethod]; !ok {
		return Output{}, fmt.Errorf("unsupported paymentMethod: %q", in.PaymentMethod)
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok || tenantID == "" {
		return Output{}, errors.New("tenant is required")
	}
	tID, err := cart.ToUUID(tenantID)
	if err != nil {
		return Output{}, fmt.Errorf("invalid tenant id: %w", err)
	}
	cID, err := cart.ToUUID(in.CartID)
	if err != nil {
		return Output{}, fmt.Errorf("invalid cart id: %w", err)
	}
	uID, err := cart.ToUUID(*userID)
	if err != nil {
		return Output{}, fmt.Errorf("invalid user id: %w", err)
	}
	addrID, err := cart.ToUUID(in.ShippingAddressID)
	if err != nil {
		return Output{}, fmt.Errorf("invalid shipping address id: %w", err)
	}

	addrRow, err := s.Q.GetAddressByID(ctx, dbgen.GetAddressByIDParams{ID: addrID, UserID: uID})
	if err != nil {
		return Output{}, fmt.Errorf("shipping address not found: %w", err)
	}
	shippingAddress := toJSON(map[string]any{
		"receiverName": textVal(addrRow.ReceiverName),
		"phone":        textVal(addrRow.Phone),
		"country":      textVal(addrRow.Country),
		"province":     textVal(addrRow.Province),
		"city":         textVal(addrRow.City),
		"postalCode":   textVal(addrRow.PostalCode),
		"addressLine1": textVal(addrRow.AddressLine1),
		"addressLine2": textVal(addrRow.AddressLine2),
	})
	shippingOption := toJSON(map[string]any{
		"service": in.ShippingService,
		"cost":    in.ShippingCost,
	})

	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Output{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()
	qtx := s.Q.WithTx(tx)

	cartRow, err := qtx.GetCartByID(ctx, cID)
	if err != nil {
		return Output{}, err
	}
	if cartRow.UserID.Valid && !cart.UUIDEqual(cartRow.UserID, uID) {
		return Output{}, errors.New("cart does not belong to user")
	}
	items, err := qtx.ListCartItems(ctx, cID)
	if err != nil {
		return Output{}, err
	}
	if len(items) == 0 {
		return Output{}, errors.New("cart is empty")
	}
	pricingItems := make([]pricing.Item, 0, len(items))
	for _, it := range items {
		pricingItems = append(pricingItems, pricing.Item{Qty: int(it.Qty), UnitPrice: pricing.Money(it.UnitPrice)})
	}
	var discount int64
	if cartRow.AppliedVoucherCode.Valid && cartRow.AppliedVoucherCode.String != "" && s.CartSvc != nil {
		discount, _, err = s.CartSvc.EvaluateVoucher(ctx, cID, cartRow.AppliedVoucherCode.String)
		if err != nil {
			discount = 0
		}
	}
	summary := pricing.Compute(pricingItems, pricing.Money(discount), s.TaxBps, pricing.Money(in.ShippingCost))

	orderNumber, err := nextOrderNumber(ctx, qtx, time.Now().UTC())
	if err != nil {
		return Output{}, err
	}

	order, err := qtx.CreateOrder(ctx, dbgen.CreateOrderParams{
		UserID:             uID,
		CartID:             cID,
		Status:             "PENDING_PAYMENT",
		Currency:           s.Currency,
		PricingSubtotal:    summary.Subtotal,
		PricingDiscount:    summary.Discount,
		PricingTax:         summary.Tax,
		PricingShipping:    summary.Shipping,
		PricingTotal:       summary.Total,
		ShippingAddress:    shippingAddress,
		ShippingOption:     shippingOption,
		Notes:              toNullableText(in.Notes),
		AppliedVoucherCode: cartRow.AppliedVoucherCode,
		TenantID:           tID,
		OrderNumber:        pgtype.Text{String: orderNumber, Valid: true},
	})
	if err != nil {
		return Output{}, err
	}
	for _, it := range items {
		if err := qtx.CreateOrderItem(ctx, dbgen.CreateOrderItemParams{
			OrderID:   order.ID,
			ProductID: it.ProductID,
			VariantID: it.VariantID,
			Title:     it.Title,
			Slug:      it.Slug,
			Qty:       it.Qty,
			UnitPrice: it.UnitPrice,
			Subtotal:  it.Subtotal,
		}); err != nil {
			return Output{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Output{}, err
	}

	out := Output{
		OrderID:       cart.UUIDString(order.ID),
		OrderNumber:   orderNumber,
		Status:        strings.ToLower(string(order.Status)),
		Total:         int64(summary.Total),
		Currency:      s.Currency,
		PaymentMethod: in.PaymentMethod,
		CreatedAt:     order.CreatedAt.Time,
	}

	if s.PaymentSvc != nil {
		if pay, perr := s.PaymentSvc.CreateIntent(ctx, out.OrderID, int64(summary.Total), in.PaymentMethod, ""); perr == nil {
			if pay.RedirectUrl.Valid {
				out.PaymentURL = pay.RedirectUrl.String
			}
			if pay.ExpiresAt.Valid {
				t := pay.ExpiresAt.Time
				out.PaymentExpiry = &t
			}
		}
	}

	if s.Events != nil {
		user, _ := s.Q.GetUserByID(ctx, uID)
		payload := map[string]any{
			"orderId":     out.OrderID,
			"orderNumber": out.OrderNumber,
			"userId":      *userID,
			"total":       summary.Total,
		}
		if user.Email != "" {
			payload["email"] = user.Email
		}
		_, _ = s.Events.Emit(ctx, events.TopicOrderCreated, order.ID, payload)
	}
	return out, nil
}

// CreateDraft computes a checkout preview for the review step. It deliberately
// persists nothing: a draft is what the shopper sees before committing, and the
// order_status enum has no DRAFT member to park a half-finished order in.
func (s *Service) CreateDraft(ctx context.Context, userID *string, in Input) (DraftOutput, error) {
	if s == nil || s.Q == nil {
		return DraftOutput{}, errors.New("checkout service not configured")
	}
	if userID == nil || *userID == "" {
		return DraftOutput{}, errors.New("user is required for checkout draft")
	}
	if in.CartID == "" {
		return DraftOutput{}, errors.New("cartId is required")
	}
	if in.ShippingAddressID == "" {
		return DraftOutput{}, errors.New("shippingAddressId is required")
	}
	if in.ShippingService == "" {
		return DraftOutput{}, errors.New("shippingService is required")
	}
	if in.ShippingCost < 0 {
		in.ShippingCost = 0
	}
	// paymentMethod is optional at draft time — the shopper may still be choosing.
	if in.PaymentMethod != "" {
		if _, ok := allowedPaymentMethods[in.PaymentMethod]; !ok {
			return DraftOutput{}, fmt.Errorf("unsupported paymentMethod: %q", in.PaymentMethod)
		}
	}

	cID, err := cart.ToUUID(in.CartID)
	if err != nil {
		return DraftOutput{}, fmt.Errorf("invalid cart id: %w", err)
	}
	uID, err := cart.ToUUID(*userID)
	if err != nil {
		return DraftOutput{}, fmt.Errorf("invalid user id: %w", err)
	}
	addrID, err := cart.ToUUID(in.ShippingAddressID)
	if err != nil {
		return DraftOutput{}, fmt.Errorf("invalid shipping address id: %w", err)
	}

	addrRow, err := s.Q.GetAddressByID(ctx, dbgen.GetAddressByIDParams{ID: addrID, UserID: uID})
	if err != nil {
		return DraftOutput{}, fmt.Errorf("shipping address not found: %w", err)
	}

	cartRow, err := s.Q.GetCartByID(ctx, cID)
	if err != nil {
		return DraftOutput{}, err
	}
	if cartRow.UserID.Valid && !cart.UUIDEqual(cartRow.UserID, uID) {
		return DraftOutput{}, errors.New("cart does not belong to user")
	}
	items, err := s.Q.ListCartItems(ctx, cID)
	if err != nil {
		return DraftOutput{}, err
	}
	if len(items) == 0 {
		return DraftOutput{}, errors.New("cart is empty")
	}

	pricingItems := make([]pricing.Item, 0, len(items))
	for _, it := range items {
		pricingItems = append(pricingItems, pricing.Item{Qty: int(it.Qty), UnitPrice: pricing.Money(it.UnitPrice)})
	}
	var discount int64
	if cartRow.AppliedVoucherCode.Valid && cartRow.AppliedVoucherCode.String != "" && s.CartSvc != nil {
		discount, _, err = s.CartSvc.EvaluateVoucher(ctx, cID, cartRow.AppliedVoucherCode.String)
		if err != nil {
			discount = 0
		}
	}
	summary := pricing.Compute(pricingItems, pricing.Money(discount), s.TaxBps, pricing.Money(in.ShippingCost))

	courier, service := splitShippingService(in.ShippingService)

	return DraftOutput{
		CartID: in.CartID,
		Address: DraftAddress{
			ReceiverName: textVal(addrRow.ReceiverName),
			Phone:        textVal(addrRow.Phone),
			AddressLine1: textVal(addrRow.AddressLine1),
			AddressLine2: textVal(addrRow.AddressLine2),
			City:         textVal(addrRow.City),
			Province:     textVal(addrRow.Province),
			PostalCode:   textVal(addrRow.PostalCode),
			Country:      textVal(addrRow.Country),
		},
		ShippingOption: DraftShippingOption{
			ID:      in.ShippingService,
			Courier: courier,
			Service: service,
			Cost:    in.ShippingCost,
		},
		PaymentMethod: in.PaymentMethod,
		Notes:         derefString(in.Notes),
		Totals: DraftTotals{
			Subtotal: int64(summary.Subtotal),
			Discount: int64(summary.Discount),
			Tax:      int64(summary.Tax),
			Shipping: int64(summary.Shipping),
			Total:    int64(summary.Total),
		},
	}, nil
}

// splitShippingService breaks a "<courier>-<service>" identifier (the shape the
// storefront builds from shipping quotes) into its parts. Unhyphenated values
// are treated as the service alone.
func splitShippingService(id string) (courier, service string) {
	if idx := strings.Index(id, "-"); idx > 0 {
		return id[:idx], id[idx+1:]
	}
	return "", id
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// nextOrderNumber allocates a per-day sequence (ORD-YYYYMMDD-NNN) within the
// current transaction. The count is scoped to the UTC day window; a unique
// partial index in the migration surfaces the rare concurrent-collision case.
func nextOrderNumber(ctx context.Context, qtx *dbgen.Queries, now time.Time) (string, error) {
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	endOfDay := startOfDay.Add(24 * time.Hour)
	count, err := qtx.CountOrdersForDay(ctx, dbgen.CountOrdersForDayParams{
		StartAt: pgtype.Timestamptz{Time: startOfDay, Valid: true},
		EndAt:   pgtype.Timestamptz{Time: endOfDay, Valid: true},
	})
	if err != nil {
		return "", fmt.Errorf("count orders for day: %w", err)
	}
	return formatOrderNumber(startOfDay, count), nil
}

// formatOrderNumber renders the contract's ORD-YYYYMMDD-NNN sequence for the
// given UTC day and zero-based same-day count.
func formatOrderNumber(day time.Time, count int64) string {
	return fmt.Sprintf("ORD-%s-%03d", day.Format("20060102"), count+1)
}

func toJSON(v any) []byte {
	if v == nil {
		return nil
	}
	b, _ := json.Marshal(v)
	return b
}

func toNullableText(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}

func textVal(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}
