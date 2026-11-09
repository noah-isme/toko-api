// Package alerts implements back-in-stock and price-drop alert subscriptions:
// the customer-facing subscribe/list/unsubscribe API (Service, Handler) and the
// background drainer that turns stock/price transitions into notifications
// (Dispatcher, Runner).
package alerts

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Alert types accepted by the API and stored in product_alert_subscriptions.
const (
	TypeBackInStock = "BACK_IN_STOCK"
	TypePriceDrop   = "PRICE_DROP"
)

// Sentinel errors returned by Service.
var (
	ErrNotFound        = errors.New("alerts: not found")
	ErrVariantMismatch = errors.New("alerts: variant does not belong to product")
	ErrAlreadyInStock  = errors.New("alerts: product is already in stock")
	ErrInvalidType     = errors.New("alerts: invalid alert type")
)

// Store is the persistence surface the subscription API needs. It is
// satisfied by *dbgen.Queries and kept narrow so tests can stub it.
type Store interface {
	GetAlertTarget(ctx context.Context, arg dbgen.GetAlertTargetParams) (dbgen.GetAlertTargetRow, error)
	UpsertAlertSubscription(ctx context.Context, arg dbgen.UpsertAlertSubscriptionParams) (dbgen.ProductAlertSubscription, error)
	ListAlertSubscriptionsForUser(ctx context.Context, arg dbgen.ListAlertSubscriptionsForUserParams) ([]dbgen.ListAlertSubscriptionsForUserRow, error)
	CountAlertSubscriptionsForUser(ctx context.Context, arg dbgen.CountAlertSubscriptionsForUserParams) (int64, error)
	DeleteAlertSubscription(ctx context.Context, arg dbgen.DeleteAlertSubscriptionParams) (int64, error)
	EnqueueAlertEvent(ctx context.Context, arg dbgen.EnqueueAlertEventParams) error
}

// SubscribeInput is a validated subscribe request. VariantID is invalid
// (Valid=false) for a product-level subscription.
type SubscribeInput struct {
	ProductID pgtype.UUID
	VariantID pgtype.UUID
	Type      string
}

// ProductSummary is the product snapshot embedded in a Subscription.
type ProductSummary struct {
	Title     string
	Slug      string
	Thumbnail pgtype.Text
	Price     int64
	InStock   bool
}

// VariantSummary is the variant snapshot embedded in a Subscription.
type VariantSummary struct {
	ID    pgtype.UUID
	SKU   pgtype.Text
	Price int64
	Stock int32
}

// Subscription is one alert subscription with its target snapshot.
type Subscription struct {
	ID               pgtype.UUID
	ProductID        pgtype.UUID
	VariantID        pgtype.UUID
	Type             string
	PriceAtSubscribe pgtype.Int8
	NotifiedAt       pgtype.Timestamptz
	CreatedAt        pgtype.Timestamptz
	Product          ProductSummary
	Variant          *VariantSummary
}

// Service implements the subscription API.
type Service struct {
	Q Store
	// Alerts, when set, is kicked after Subscribe enqueues a re-check event.
	Alerts Kicker
}

// ValidType reports whether t is a supported alert type.
func ValidType(t string) bool {
	return t == TypeBackInStock || t == TypePriceDrop
}

// Sellable is exactly the cart AddItem rule (internal/cart/service.go): a
// variant line is accepted when the variant has stock (products.in_stock is
// not consulted); a product-level line is accepted when products.in_stock is
// true. ListPendingBackInStockSubscriptions uses the same predicate.
func Sellable(inStock bool, hasVariant bool, variantStock int32) bool {
	if hasVariant {
		return variantStock > 0
	}
	return inStock
}

// Subscribe creates (created=true) or re-arms (created=false) the caller's
// subscription for the target.
func (s *Service) Subscribe(ctx context.Context, userID, tenantID pgtype.UUID, in SubscribeInput) (Subscription, bool, error) {
	if !ValidType(in.Type) {
		return Subscription{}, false, ErrInvalidType
	}
	target, err := s.Q.GetAlertTarget(ctx, dbgen.GetAlertTargetParams{
		VariantID: in.VariantID,
		ProductID: in.ProductID,
		TenantID:  tenantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Subscription{}, false, ErrNotFound
		}
		return Subscription{}, false, fmt.Errorf("load alert target: %w", err)
	}
	hasVariant := in.VariantID.Valid
	if hasVariant && !target.VariantID.Valid {
		return Subscription{}, false, ErrVariantMismatch
	}

	var price pgtype.Int8
	switch in.Type {
	case TypeBackInStock:
		if Sellable(target.InStock, hasVariant, target.VariantStock.Int32) {
			return Subscription{}, false, ErrAlreadyInStock
		}
	case TypePriceDrop:
		price = pgtype.Int8{Int64: target.Price, Valid: true}
		if hasVariant {
			price = target.VariantPrice
		}
	}

	row, err := s.Q.UpsertAlertSubscription(ctx, dbgen.UpsertAlertSubscriptionParams{
		TenantID:         tenantID,
		UserID:           userID,
		ProductID:        in.ProductID,
		VariantID:        in.VariantID,
		AlertType:        in.Type,
		PriceAtSubscribe: price,
	})
	if err != nil {
		return Subscription{}, false, fmt.Errorf("upsert alert subscription: %w", err)
	}
	// A fresh insert takes both timestamps from the same now(); a re-arm
	// bumps updated_at only.
	created := row.CreatedAt.Time.Equal(row.UpdatedAt.Time)

	if err := s.recheck(ctx, tenantID, in, row.PriceAtSubscribe); err != nil {
		return Subscription{}, false, err
	}

	sub := Subscription{
		ID:               row.ID,
		ProductID:        row.ProductID,
		VariantID:        row.VariantID,
		Type:             row.AlertType,
		PriceAtSubscribe: row.PriceAtSubscribe,
		NotifiedAt:       row.NotifiedAt,
		CreatedAt:        row.CreatedAt,
		Product: ProductSummary{
			Title:     target.Title,
			Slug:      target.Slug,
			Thumbnail: target.Thumbnail,
			Price:     target.Price,
			InStock:   target.InStock,
		},
	}
	if hasVariant {
		sub.Variant = &VariantSummary{
			ID:    target.VariantID,
			SKU:   target.VariantSku,
			Price: target.VariantPrice.Int64,
			Stock: target.VariantStock.Int32,
		}
	}
	return sub, created, nil
}

// recheck closes the race between the eligibility check and the commit of
// the subscription: if a restock (or price drop) committed and its outbox
// event was drained before this subscription became visible, the drainer
// never saw it. Re-reading the target after commit and enqueueing a fresh
// event when it is already actionable guarantees the drainer evaluates the
// subscription at least once more; a restock committed after this read
// produces its own event, which will see the subscription.
func (s *Service) recheck(ctx context.Context, tenantID pgtype.UUID, in SubscribeInput, priceAtSubscribe pgtype.Int8) error {
	target, err := s.Q.GetAlertTarget(ctx, dbgen.GetAlertTargetParams{
		VariantID: in.VariantID,
		ProductID: in.ProductID,
		TenantID:  tenantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // product deleted meanwhile; the subscription cascades
		}
		return fmt.Errorf("recheck alert target: %w", err)
	}
	hasVariant := in.VariantID.Valid
	actionable := false
	switch in.Type {
	case TypeBackInStock:
		actionable = Sellable(target.InStock, hasVariant, target.VariantStock.Int32)
	case TypePriceDrop:
		current := target.Price
		if hasVariant {
			current = target.VariantPrice.Int64
		}
		actionable = priceAtSubscribe.Valid && current < priceAtSubscribe.Int64
	}
	if !actionable {
		return nil
	}
	if err := s.Q.EnqueueAlertEvent(ctx, dbgen.EnqueueAlertEventParams{
		TenantID:  tenantID,
		ProductID: in.ProductID,
		VariantID: in.VariantID,
		AlertType: in.Type,
	}); err != nil {
		return fmt.Errorf("enqueue alert recheck: %w", err)
	}
	if s.Alerts != nil {
		s.Alerts.Kick()
	}
	return nil
}

// List returns a page of the caller's subscriptions (newest first) and the
// total count. productID may be invalid to list all products.
func (s *Service) List(ctx context.Context, userID, tenantID, productID pgtype.UUID, page, limit int32) ([]Subscription, int64, error) {
	if page < 1 {
		page = 1
	}
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	// Keep (page-1)*limit inside int32; pages past the end are empty anyway.
	if maxPage := int32(math.MaxInt32 / limit); page > maxPage {
		page = maxPage
	}
	rows, err := s.Q.ListAlertSubscriptionsForUser(ctx, dbgen.ListAlertSubscriptionsForUserParams{
		TenantID:  tenantID,
		UserID:    userID,
		ProductID: productID,
		RowLimit:  limit,
		RowOffset: (page - 1) * limit,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("list alert subscriptions: %w", err)
	}
	total, err := s.Q.CountAlertSubscriptionsForUser(ctx, dbgen.CountAlertSubscriptionsForUserParams{
		TenantID:  tenantID,
		UserID:    userID,
		ProductID: productID,
	})
	if err != nil {
		return nil, 0, fmt.Errorf("count alert subscriptions: %w", err)
	}
	out := make([]Subscription, 0, len(rows))
	for _, r := range rows {
		sub := Subscription{
			ID:               r.ID,
			ProductID:        r.ProductID,
			VariantID:        r.VariantID,
			Type:             r.AlertType,
			PriceAtSubscribe: r.PriceAtSubscribe,
			NotifiedAt:       r.NotifiedAt,
			CreatedAt:        r.CreatedAt,
			Product: ProductSummary{
				Title:     r.ProductTitle,
				Slug:      r.ProductSlug,
				Thumbnail: r.ProductThumbnail,
				Price:     r.ProductPrice,
				InStock:   r.ProductInStock,
			},
		}
		if r.VariantID.Valid {
			sub.Variant = &VariantSummary{
				ID:    r.VariantID,
				SKU:   r.VariantSku,
				Price: r.VariantPrice.Int64,
				Stock: r.VariantStock.Int32,
			}
		}
		out = append(out, sub)
	}
	return out, total, nil
}

// Unsubscribe deletes the caller's subscription. It reports false when no
// subscription with that id belongs to the caller in this tenant.
func (s *Service) Unsubscribe(ctx context.Context, id, userID, tenantID pgtype.UUID) (bool, error) {
	n, err := s.Q.DeleteAlertSubscription(ctx, dbgen.DeleteAlertSubscriptionParams{
		ID:       id,
		TenantID: tenantID,
		UserID:   userID,
	})
	if err != nil {
		return false, fmt.Errorf("delete alert subscription: %w", err)
	}
	return n > 0, nil
}
