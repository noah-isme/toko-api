package cart

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// ErrNotFound indicates the requested cart could not be located.
var ErrNotFound = errors.New("cart not found")

// ErrInvalidInput is returned when the provided payload is invalid.
var ErrInvalidInput = errors.New("invalid input")

// Service encapsulates cart domain operations.
type Service struct {
	Q                          *dbgen.Queries
	TTL                        time.Duration
	Now                        func() time.Time
	VoucherPerUserLimitDefault int
}

func (s *Service) ttl() time.Duration {
	if s == nil || s.TTL <= 0 {
		return 7 * 24 * time.Hour
	}
	return s.TTL
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// EnsureCart loads or creates a cart for the provided identifiers.
func (s *Service) EnsureCart(ctx context.Context, userID *string, anonID *string) (dbgen.Cart, error) {
	if s == nil || s.Q == nil {
		return dbgen.Cart{}, errors.New("cart service not configured")
	}
	var (
		cart dbgen.Cart
		err  error
	)
	ttl := s.ttl()
	expires := pgtype.Timestamptz{Time: s.now().Add(ttl), Valid: true}

	if userID != nil && *userID != "" {
		uid, err := toUUID(*userID)
		if err != nil {
			return dbgen.Cart{}, fmt.Errorf("parse user id: %w", err)
		}
		cart, err = s.Q.GetActiveCartByUser(ctx, uid)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				cart, err = s.Q.CreateCart(ctx, dbgen.CreateCartParams{
					UserID:    uid,
					AnonID:    pgtype.Text{},
					ExpiresAt: expires,
				})
				return cart, err
			}
			return dbgen.Cart{}, err
		}
		_ = s.Q.TouchCart(ctx, dbgen.TouchCartParams{ID: cart.ID, ExpiresAt: expires})
		return cart, nil
	}

	if anonID != nil && *anonID != "" {
		cart, err = s.Q.GetActiveCartByAnon(ctx, pgtype.Text{String: *anonID, Valid: true})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				cart, err = s.Q.CreateCart(ctx, dbgen.CreateCartParams{
					UserID:    pgtype.UUID{},
					AnonID:    pgtype.Text{String: *anonID, Valid: true},
					ExpiresAt: expires,
				})
				return cart, err
			}
			return dbgen.Cart{}, err
		}
		_ = s.Q.TouchCart(ctx, dbgen.TouchCartParams{ID: cart.ID, ExpiresAt: expires})
		return cart, nil
	}

	return dbgen.Cart{}, ErrInvalidInput
}

// AddItem inserts or increments a cart item.
func (s *Service) AddItem(ctx context.Context, cartID string, productID string, variantID *string, qty int) error {
	if s == nil || s.Q == nil {
		return errors.New("cart service not configured")
	}
	if qty <= 0 {
		return fmt.Errorf("qty must be positive: %w", ErrInvalidInput)
	}
	cID, err := toUUID(cartID)
	if err != nil {
		return fmt.Errorf("parse cart id: %w", err)
	}
	pID, err := toUUID(productID)
	if err != nil {
		return fmt.Errorf("parse product id: %w", err)
	}
	var vID pgtype.UUID
	if variantID != nil && *variantID != "" {
		vID, err = toUUID(*variantID)
		if err != nil {
			return fmt.Errorf("parse variant id: %w", err)
		}
	}

	expires := pgtype.Timestamptz{Time: s.now().Add(s.ttl()), Valid: true}
	item, err := s.Q.FindCartItemByProductVariant(ctx, dbgen.FindCartItemByProductVariantParams{
		CartID:    cID,
		ProductID: pID,
		VariantID: vID,
	})
	if err == nil {
		newQty := item.Qty + int32(qty)
		newSubtotal := int64(newQty) * item.UnitPrice
		if _, err := s.Q.UpdateCartItemQty(ctx, dbgen.UpdateCartItemQtyParams{ID: item.ID, Qty: newQty, Subtotal: newSubtotal}); err != nil {
			return err
		}
		_ = s.Q.TouchCart(ctx, dbgen.TouchCartParams{ID: cID, ExpiresAt: expires})
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	product, err := s.Q.GetProductForCart(ctx, pID)
	if err != nil {
		return err
	}
	unitPrice := product.Price
	if vID.Valid {
		variant, err := s.Q.GetVariantForCart(ctx, vID)
		if err != nil {
			return err
		}
		if !uuidEqual(variant.ProductID, pID) {
			return fmt.Errorf("variant does not belong to product: %w", ErrInvalidInput)
		}
		unitPrice = variant.Price
		if variant.Stock <= 0 {
			return fmt.Errorf("variant out of stock: %w", ErrInvalidInput)
		}
	}
	if unitPrice < 0 {
		unitPrice = 0
	}
	subtotal := int64(qty) * unitPrice
	if subtotal < 0 {
		subtotal = 0
	}
	if _, err := s.Q.CreateCartItem(ctx, dbgen.CreateCartItemParams{
		CartID:    cID,
		ProductID: pID,
		VariantID: vID,
		Title:     product.Title,
		Slug:      product.Slug,
		Qty:       int32(qty),
		UnitPrice: unitPrice,
		Subtotal:  subtotal,
	}); err != nil {
		return err
	}
	_ = s.Q.TouchCart(ctx, dbgen.TouchCartParams{ID: cID, ExpiresAt: expires})
	return nil
}

// UpdateQty updates the quantity for a cart item.
func (s *Service) UpdateQty(ctx context.Context, itemID string, qty int) error {
	if s == nil || s.Q == nil {
		return errors.New("cart service not configured")
	}
	if qty <= 0 {
		return fmt.Errorf("qty must be positive: %w", ErrInvalidInput)
	}
	id, err := toUUID(itemID)
	if err != nil {
		return fmt.Errorf("parse item id: %w", err)
	}
	item, err := s.Q.GetCartItemByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	newSubtotal := int64(qty) * item.UnitPrice
	_, err = s.Q.UpdateCartItemQty(ctx, dbgen.UpdateCartItemQtyParams{ID: item.ID, Qty: int32(qty), Subtotal: newSubtotal})
	if err != nil {
		return err
	}
	expires := pgtype.Timestamptz{Time: s.now().Add(s.ttl()), Valid: true}
	_ = s.Q.TouchCart(ctx, dbgen.TouchCartParams{ID: item.CartID, ExpiresAt: expires})
	return nil
}

// RemoveItem deletes a cart item.
func (s *Service) RemoveItem(ctx context.Context, cartID string, itemID string) error {
	if s == nil || s.Q == nil {
		return errors.New("cart service not configured")
	}
	cID, err := toUUID(cartID)
	if err != nil {
		return fmt.Errorf("parse cart id: %w", err)
	}
	iID, err := toUUID(itemID)
	if err != nil {
		return fmt.Errorf("parse item id: %w", err)
	}
	if err := s.Q.DeleteCartItem(ctx, dbgen.DeleteCartItemParams{ID: iID, CartID: cID}); err != nil {
		return err
	}
	expires := pgtype.Timestamptz{Time: s.now().Add(s.ttl()), Valid: true}
	_ = s.Q.TouchCart(ctx, dbgen.TouchCartParams{ID: cID, ExpiresAt: expires})
	return nil
}

// ApplyVoucher validates and attaches a voucher to the cart returning the applied discount amount.
func (s *Service) ApplyVoucher(ctx context.Context, cartID string, code string) (int64, error) {
	if s == nil || s.Q == nil {
		return 0, errors.New("cart service not configured")
	}
	if code == "" {
		return 0, fmt.Errorf("voucher code required: %w", ErrInvalidInput)
	}
	cID, err := toUUID(cartID)
	if err != nil {
		return 0, fmt.Errorf("parse cart id: %w", err)
	}
	cart, err := s.Q.GetCartByID(ctx, cID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	discount, voucher, err := s.evaluateVoucher(ctx, cart, code)
	if err != nil {
		return 0, err
	}
	if err := s.Q.UpdateCartVoucher(ctx, dbgen.UpdateCartVoucherParams{ID: cart.ID, AppliedVoucherCode: pgtype.Text{String: voucher.Code, Valid: true}}); err != nil {
		return 0, err
	}
	expires := pgtype.Timestamptz{Time: s.now().Add(s.ttl()), Valid: true}
	_ = s.Q.TouchCart(ctx, dbgen.TouchCartParams{ID: cart.ID, ExpiresAt: expires})
	return discount, nil
}

// RemoveVoucher clears an applied voucher.
func (s *Service) RemoveVoucher(ctx context.Context, cartID string) error {
	if s == nil || s.Q == nil {
		return errors.New("cart service not configured")
	}
	cID, err := toUUID(cartID)
	if err != nil {
		return fmt.Errorf("parse cart id: %w", err)
	}
	if err := s.Q.UpdateCartVoucher(ctx, dbgen.UpdateCartVoucherParams{ID: cID, AppliedVoucherCode: pgtype.Text{}}); err != nil {
		return err
	}
	expires := pgtype.Timestamptz{Time: s.now().Add(s.ttl()), Valid: true}
	_ = s.Q.TouchCart(ctx, dbgen.TouchCartParams{ID: cID, ExpiresAt: expires})
	return nil
}

// Merge moves guest cart items into the user's active cart returning the resulting cart identifier.
func (s *Service) Merge(ctx context.Context, guestCartID string, userID string) (string, error) {
	if s == nil || s.Q == nil {
		return "", errors.New("cart service not configured")
	}
	gID, err := toUUID(guestCartID)
	if err != nil {
		return "", fmt.Errorf("parse guest cart id: %w", err)
	}
	uID, err := toUUID(userID)
	if err != nil {
		return "", fmt.Errorf("parse user id: %w", err)
	}
	guestCart, err := s.Q.GetCartByID(ctx, gID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	userIDCopy := userID
	userCart, err := s.EnsureCart(ctx, &userIDCopy, nil)
	if err != nil {
		return "", err
	}
	guestItems, err := s.Q.ListCartItems(ctx, gID)
	if err != nil {
		return "", err
	}
	for _, item := range guestItems {
		existing, err := s.Q.FindCartItemByProductVariant(ctx, dbgen.FindCartItemByProductVariantParams{
			CartID:    userCart.ID,
			ProductID: item.ProductID,
			VariantID: item.VariantID,
		})
		if err == nil {
			if existing.Qty < item.Qty {
				_, err = s.Q.UpdateCartItemQty(ctx, dbgen.UpdateCartItemQtyParams{ID: existing.ID, Qty: item.Qty, Subtotal: int64(item.Qty) * existing.UnitPrice})
				if err != nil {
					return "", err
				}
			}
			continue
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return "", err
		}
		if _, err := s.Q.CreateCartItem(ctx, dbgen.CreateCartItemParams{
			CartID:    userCart.ID,
			ProductID: item.ProductID,
			VariantID: item.VariantID,
			Title:     item.Title,
			Slug:      item.Slug,
			Qty:       item.Qty,
			UnitPrice: item.UnitPrice,
			Subtotal:  item.Subtotal,
		}); err != nil {
			return "", err
		}
	}
	expires := pgtype.Timestamptz{Time: s.now().Add(s.ttl()), Valid: true}
	_ = s.Q.TouchCart(ctx, dbgen.TouchCartParams{ID: userCart.ID, ExpiresAt: expires})
	_ = s.Q.TouchCart(ctx, dbgen.TouchCartParams{ID: guestCart.ID, ExpiresAt: pgtype.Timestamptz{Time: s.now(), Valid: true}})
	_ = s.Q.UpdateCartVoucher(ctx, dbgen.UpdateCartVoucherParams{ID: guestCart.ID, AppliedVoucherCode: pgtype.Text{}})
	_ = s.Q.TransferCartToUser(ctx, dbgen.TransferCartToUserParams{ID: guestCart.ID, UserID: uID})
	return uuidString(userCart.ID), nil
}

func (s *Service) itemEligible(ctx context.Context, item dbgen.CartItem, voucher dbgen.Voucher) (bool, error) {
	if len(voucher.ProductIds) == 0 && len(voucher.CategoryIds) == 0 && len(voucher.BrandIds) == 0 {
		return true, nil
	}
	product, err := s.Q.GetProductForCart(ctx, item.ProductID)
	if err != nil {
		return false, err
	}
	if len(voucher.ProductIds) > 0 {
		for _, el := range voucher.ProductIds {
			if uuidEqual(el, item.ProductID) {
				return true, nil
			}
		}
	}
	if len(voucher.CategoryIds) > 0 && product.CategoryID.Valid {
		for _, el := range voucher.CategoryIds {
			if uuidEqual(el, product.CategoryID) {
				return true, nil
			}
		}
	}
	if len(voucher.BrandIds) > 0 && product.BrandID.Valid {
		for _, el := range voucher.BrandIds {
			if uuidEqual(el, product.BrandID) {
				return true, nil
			}
		}
	}
	return false, nil
}

func (s *Service) evaluateVoucher(ctx context.Context, cart dbgen.Cart, code string) (int64, dbgen.Voucher, error) {
	if code == "" {
		return 0, dbgen.Voucher{}, fmt.Errorf("voucher code required: %w", ErrInvalidInput)
	}
	items, subtotal, err := s.loadCartItems(ctx, cart.ID)
	if err != nil {
		return 0, dbgen.Voucher{}, err
	}
	voucher, err := s.Q.GetVoucherByCode(ctx, code)
	if err != nil {
		return 0, dbgen.Voucher{}, err
	}
	now := s.now()
	if voucher.ValidFrom.Valid && voucher.ValidFrom.Time.After(now) {
		return 0, dbgen.Voucher{}, fmt.Errorf("voucher not active: %w", ErrInvalidInput)
	}
	if voucher.ValidTo.Valid && voucher.ValidTo.Time.Before(now) {
		return 0, dbgen.Voucher{}, fmt.Errorf("voucher expired: %w", ErrInvalidInput)
	}
	if voucher.UsageLimit.Valid && voucher.UsedCount >= voucher.UsageLimit.Int32 {
		return 0, dbgen.Voucher{}, fmt.Errorf("voucher usage exceeded: %w", ErrInvalidInput)
	}
	limit := int32(s.VoucherPerUserLimitDefault)
	if voucher.PerUserLimit.Valid {
		limit = voucher.PerUserLimit.Int32
	}
	if limit > 0 && cart.UserID.Valid {
		used, err := s.Q.CountVoucherUsageByUser(ctx, dbgen.CountVoucherUsageByUserParams{VoucherID: voucher.ID, UserID: cart.UserID})
		if err != nil {
			return 0, dbgen.Voucher{}, err
		}
		if int32(used) >= limit {
			return 0, dbgen.Voucher{}, fmt.Errorf("voucher usage exceeded: %w", ErrInvalidInput)
		}
	}
	if subtotal < voucher.MinSpend {
		return 0, dbgen.Voucher{}, fmt.Errorf("minimum spend not met: %w", ErrInvalidInput)
	}

	eligible := subtotal
	hasScope := len(voucher.ProductIds) > 0 || len(voucher.CategoryIds) > 0
	hasBrandScope := len(voucher.BrandIds) > 0
	if hasScope || hasBrandScope {
		eligible = 0
		for _, it := range items {
			allowed, err := s.itemEligible(ctx, it, voucher)
			if err != nil {
				return 0, dbgen.Voucher{}, err
			}
			if allowed {
				eligible += it.Subtotal
			}
		}
		if eligible == 0 {
			return 0, dbgen.Voucher{}, fmt.Errorf("voucher not applicable: %w", ErrInvalidInput)
		}
	}

	var discount int64
	switch voucher.Kind {
	case dbgen.DiscountKindPercent:
		if !voucher.PercentBps.Valid || voucher.PercentBps.Int32 <= 0 {
			return 0, dbgen.Voucher{}, fmt.Errorf("invalid percent voucher: %w", ErrInvalidInput)
		}
		discount = (eligible * int64(voucher.PercentBps.Int32)) / 10000
	default:
		discount = voucher.Value
	}
	if discount > eligible {
		discount = eligible
	}
	if discount < 0 {
		discount = 0
	}
	return discount, voucher, nil
}

func (s *Service) loadCartItems(ctx context.Context, cartID pgtype.UUID) ([]dbgen.CartItem, int64, error) {
	items, err := s.Q.ListCartItems(ctx, cartID)
	if err != nil {
		return nil, 0, err
	}
	if len(items) == 0 {
		return nil, 0, fmt.Errorf("cart empty: %w", ErrInvalidInput)
	}
	var subtotal int64
	for _, it := range items {
		subtotal += it.Subtotal
	}
	return items, subtotal, nil
}

func toUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	parsed, err := uuid.Parse(value)
	if err != nil {
		return id, err
	}
	if err := id.Scan(parsed[:]); err != nil {
		return pgtype.UUID{}, err
	}
	return id, nil
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

// ToUUID converts a string representation of a UUID into pgtype.UUID.
func ToUUID(value string) (pgtype.UUID, error) {
	return toUUID(value)
}

// UUIDString converts a pgtype.UUID into a canonical string.
func UUIDString(id pgtype.UUID) string {
	return uuidString(id)
}

// UUIDEqual reports whether two UUID values are both valid and identical.
func UUIDEqual(a, b pgtype.UUID) bool {
	return uuidEqual(a, b)
}

// EvaluateVoucher exposes voucher calculation for other services without mutating state.
func (s *Service) EvaluateVoucher(ctx context.Context, cartID pgtype.UUID, code string) (int64, dbgen.Voucher, error) {
	if s == nil {
		return 0, dbgen.Voucher{}, errors.New("cart service not configured")
	}
	cart, err := s.Q.GetCartByID(ctx, cartID)
	if err != nil {
		return 0, dbgen.Voucher{}, err
	}
	return s.evaluateVoucher(ctx, cart, code)
}

func uuidEqual(a, b pgtype.UUID) bool {
	if !a.Valid || !b.Valid {
		return false
	}
	return a.Bytes == b.Bytes
}
