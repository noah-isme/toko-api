package checkout

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/cart"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/pricing"
)

type Addr struct {
	ReceiverName string `json:"receiverName"`
	Phone        string `json:"phone"`
	Country      string `json:"country"`
	Province     string `json:"province"`
	City         string `json:"city"`
	PostalCode   string `json:"postalCode"`
	AddressLine1 string `json:"addressLine1"`
	AddressLine2 string `json:"addressLine2"`
}

type ShipOpt struct {
	Courier string `json:"courier"`
	Service string `json:"service"`
	Price   int64  `json:"price"`
	ETD     string `json:"etd"`
}

type Input struct {
	CartID         string  `json:"cartId"`
	Address        Addr    `json:"address"`
	Shipping       ShipOpt `json:"shipping"`
	Notes          *string `json:"notes"`
	PaymentChannel *string `json:"paymentChannel"`
}

type Output struct {
	OrderID string `json:"orderId"`
	Status  string `json:"status"`
	Payment struct {
		Provider    string `json:"provider"`
		Token       string `json:"token"`
		RedirectURL string `json:"redirectUrl"`
	} `json:"payment"`
}

type Service struct {
	Q        *dbgen.Queries
	Pool     *pgxpool.Pool
	CartSvc  *cart.Service
	TaxBps   int
	Currency string
	Events   *events.Bus
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
	cID, err := cart.ToUUID(in.CartID)
	if err != nil {
		return Output{}, fmt.Errorf("invalid cart id: %w", err)
	}
	uID, err := cart.ToUUID(*userID)
	if err != nil {
		return Output{}, fmt.Errorf("invalid user id: %w", err)
	}
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
	shippingCost := in.Shipping.Price
	if shippingCost < 0 {
		shippingCost = 0
	}
	summary := pricing.Compute(pricingItems, pricing.Money(discount), s.TaxBps, pricing.Money(shippingCost))
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
		ShippingAddress:    toJSON(in.Address),
		ShippingOption:     toJSON(in.Shipping),
		Notes:              toNullableText(in.Notes),
		AppliedVoucherCode: cartRow.AppliedVoucherCode,
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
	if s.Events != nil {
		user, _ := s.Q.GetUserByID(ctx, uID)
		payload := map[string]any{
			"orderId": cart.UUIDString(order.ID),
			"userId":  *userID,
			"total":   summary.Total,
		}
		if user.Email != "" {
			payload["email"] = user.Email
		}
		_, _ = s.Events.Emit(ctx, events.TopicOrderCreated, order.ID, payload)
	}
	var out Output
	out.OrderID = cart.UUIDString(order.ID)
	out.Status = string(order.Status)
	out.Payment.Provider = ""
	out.Payment.Token = ""
	out.Payment.RedirectURL = ""
	return out, nil
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
