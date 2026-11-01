// Package reservation releases the stock and flash-sale quota an unpaid order
// holds.
//
// Lock order. Every transaction that touches settlement rows takes locks in
// this order, so none of them can deadlock with another:
//
//  1. orders row (GetOrderByIDForUpdate / LockPendingOrderForExpiry)
//  2. payments rows of that order
//  3. inventory_reservations rows, ascending variant_id
//  4. product_variants rows, ascending variant_id
//  5. flash_sale_order_items, then flash_sale_items in ascending id
//  6. vouchers
//
// Checkout follows steps 4 and 5 for the order it creates (all variants first,
// then all flash-sale items). The orphan sweep (checkout-expiry Phase A) holds
// no order lock: it only touches reservations of orders that are already
// CANCELLED or EXPIRED and uses SKIP LOCKED, then steps 4 and 5.
package reservation

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Queries is the subset of generated queries the release needs. *dbgen.Queries
// (bound to a transaction) satisfies it.
type Queries interface {
	ListReservedInventoryByOrderForUpdate(ctx context.Context, orderID pgtype.UUID) ([]dbgen.InventoryReservation, error)
	TransitionInventoryReservation(ctx context.Context, arg dbgen.TransitionInventoryReservationParams) (dbgen.InventoryReservation, error)
	ReleaseVariantStock(ctx context.Context, arg dbgen.ReleaseVariantStockParams) error
	LockFlashSaleItemsForOrders(ctx context.Context, orderIDs []pgtype.UUID) ([]pgtype.UUID, error)
	ReleaseFlashSaleReservations(ctx context.Context, orderID pgtype.UUID) error
}

// ReleaseOrder moves every RESERVED reservation of the order to `to`
// (RELEASED or EXPIRED), returns the stock to its variant, and releases the
// order's reserved flash-sale quota. The caller must already hold the order's
// row lock inside the same transaction. It returns the number of reservations
// released.
func ReleaseOrder(ctx context.Context, q Queries, orderID pgtype.UUID, to dbgen.InventoryReservationStatus) (int, error) {
	rows, err := q.ListReservedInventoryByOrderForUpdate(ctx, orderID)
	if err != nil {
		return 0, fmt.Errorf("list reservations: %w", err)
	}
	released := 0
	for _, r := range rows {
		if _, err := q.TransitionInventoryReservation(ctx, dbgen.TransitionInventoryReservationParams{
			ID: r.ID, FromStatus: dbgen.InventoryReservationStatusRESERVED, ToStatus: to,
		}); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			return released, fmt.Errorf("transition reservation: %w", err)
		}
		if err := q.ReleaseVariantStock(ctx, dbgen.ReleaseVariantStockParams{ID: r.VariantID, Qty: r.Qty}); err != nil {
			return released, fmt.Errorf("release variant stock: %w", err)
		}
		released++
	}
	if err := ReleaseFlashSale(ctx, q, []pgtype.UUID{orderID}); err != nil {
		return released, err
	}
	return released, nil
}

// ReleaseFlashSale locks the flash-sale items the orders hold quota on (in
// ascending id) and releases that quota. Call it after all variant rows.
func ReleaseFlashSale(ctx context.Context, q Queries, orderIDs []pgtype.UUID) error {
	if len(orderIDs) == 0 {
		return nil
	}
	if _, err := q.LockFlashSaleItemsForOrders(ctx, orderIDs); err != nil {
		return fmt.Errorf("lock flash-sale items: %w", err)
	}
	for _, id := range orderIDs {
		if err := q.ReleaseFlashSaleReservations(ctx, id); err != nil {
			return fmt.Errorf("release flash-sale quota: %w", err)
		}
	}
	return nil
}
