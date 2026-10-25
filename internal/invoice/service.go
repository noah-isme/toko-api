package invoice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Sentinel errors returned by Service.Build.
var (
	ErrNotFound    = errors.New("invoice: order not found")
	ErrNotEligible = errors.New("invoice: order is not eligible for an invoice")
)

// storeSettingKeys are the tenant_settings keys written by the admin
// settings page that the invoice header uses.
var storeSettingKeys = []string{"storeName", "contactEmail", "contactPhone", "description"}

// TxBeginner starts a database transaction. *pgxpool.Pool satisfies it.
type TxBeginner interface {
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Service assembles invoice documents and assigns invoice numbers.
type Service struct {
	Q        *dbgen.Queries
	Pool     TxBeginner
	Renderer *Renderer
	Now      func() time.Time
}

// Result is a rendered invoice.
type Result struct {
	Number   string
	Filename string
	PDF      []byte
}

// Eligible reports whether an order in this status may receive a NEW invoice
// number: paid or any later fulfilment state. PENDING_PAYMENT and CANCELLED
// are not eligible; an invoice already issued stays downloadable regardless.
func Eligible(status dbgen.OrderStatus) bool {
	switch status {
	case dbgen.OrderStatusPAID, dbgen.OrderStatusPACKED, dbgen.OrderStatusSHIPPED,
		dbgen.OrderStatusOUTFORDELIVERY, dbgen.OrderStatusDELIVERED:
		return true
	default:
		return false
	}
}

// invoiceNumber formats INV-YYYY-NNNNNN.
func invoiceNumber(year int, n int64) string {
	return fmt.Sprintf("INV-%04d-%06d", year, n)
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func loadLocation(name string) *time.Location {
	if strings.TrimSpace(name) == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.UTC
	}
	return loc
}

// Build renders the invoice for an order in tenantID. userID, when valid,
// restricts the lookup to that owner (customer endpoint); admin callers pass
// an invalid UUID.
func (s *Service) Build(ctx context.Context, tenantID, orderID, userID pgtype.UUID) (Result, error) {
	if s == nil || s.Q == nil || s.Pool == nil {
		return Result{}, errors.New("invoice: service not configured")
	}
	row, err := s.Q.GetOrderForInvoice(ctx, dbgen.GetOrderForInvoiceParams{
		ID:       orderID,
		TenantID: tenantID,
		UserID:   userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Result{}, ErrNotFound
		}
		return Result{}, fmt.Errorf("invoice: load order: %w", err)
	}
	loc := loadLocation(row.TenantTimezone)

	number, issuedAt := row.InvoiceNumber.String, row.IssuedAt.Time
	if !row.InvoiceNumber.Valid {
		if !Eligible(row.Status) {
			return Result{}, ErrNotEligible
		}
		number, issuedAt, err = s.assignNumber(ctx, tenantID, orderID, loc)
		if err != nil {
			return Result{}, err
		}
	}

	items, err := s.Q.ListInvoiceItems(ctx, dbgen.ListInvoiceItemsParams{OrderID: orderID, TenantID: tenantID})
	if err != nil {
		return Result{}, fmt.Errorf("invoice: load items: %w", err)
	}
	settings, err := s.Q.ListTenantSettingsByKeys(ctx, dbgen.ListTenantSettingsByKeysParams{TenantID: tenantID, Keys: storeSettingKeys})
	if err != nil {
		return Result{}, fmt.Errorf("invoice: load store settings: %w", err)
	}

	doc := buildDocument(row, items, settingsMap(settings))
	doc.Number = number
	doc.IssuedAt = issuedAt
	doc.Location = loc

	renderer := s.Renderer
	if renderer == nil {
		renderer = NewRenderer()
	}
	pdf, err := renderer.Render(doc)
	if err != nil {
		return Result{}, err
	}
	return Result{Number: number, Filename: number + ".pdf", PDF: pdf}, nil
}

// assignNumber allocates the order's invoice number exactly once. The tenant's
// counter row lock serializes concurrent first requests; whoever acquires it
// second re-reads order_invoices and reuses the winner's number, so no number
// is burned and an order never gets two.
func (s *Service) assignNumber(ctx context.Context, tenantID, orderID pgtype.UUID, loc *time.Location) (string, time.Time, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("invoice: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	qtx := s.Q.WithTx(tx)

	if _, err := qtx.LockInvoiceCounter(ctx, tenantID); err != nil {
		return "", time.Time{}, fmt.Errorf("invoice: lock counter: %w", err)
	}
	existing, err := qtx.GetOrderInvoice(ctx, dbgen.GetOrderInvoiceParams{OrderID: orderID, TenantID: tenantID})
	switch {
	case err == nil:
		return existing.InvoiceNumber, existing.IssuedAt.Time, tx.Commit(ctx)
	case !errors.Is(err, pgx.ErrNoRows):
		return "", time.Time{}, fmt.Errorf("invoice: read existing: %w", err)
	}
	// Eligibility was checked before the transaction; re-check under a row
	// lock so an order cancelled at the same moment is not issued a number.
	status, err := qtx.GetOrderStatusForInvoice(ctx, dbgen.GetOrderStatusForInvoiceParams{OrderID: orderID, TenantID: tenantID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", time.Time{}, ErrNotFound
		}
		return "", time.Time{}, fmt.Errorf("invoice: recheck status: %w", err)
	}
	if !Eligible(status) {
		return "", time.Time{}, ErrNotEligible
	}
	n, err := qtx.IncrementInvoiceCounter(ctx, tenantID)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("invoice: increment counter: %w", err)
	}
	now := s.now()
	inserted, err := qtx.InsertOrderInvoice(ctx, dbgen.InsertOrderInvoiceParams{
		OrderID:       orderID,
		TenantID:      tenantID,
		InvoiceNumber: invoiceNumber(now.In(loc).Year(), n),
		IssuedAt:      pgtype.Timestamptz{Time: now, Valid: true},
	})
	if err != nil {
		return "", time.Time{}, fmt.Errorf("invoice: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", time.Time{}, fmt.Errorf("invoice: commit: %w", err)
	}
	return inserted.InvoiceNumber, inserted.IssuedAt.Time, nil
}

// settingsMap decodes tenant_settings values that are JSON strings.
func settingsMap(rows []dbgen.ListTenantSettingsByKeysRow) map[string]string {
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		var v string
		if err := json.Unmarshal(r.Value, &v); err == nil {
			if v = strings.TrimSpace(v); v != "" {
				out[r.Key] = v
			}
		}
	}
	return out
}

// buildDocument copies order data verbatim (amounts are whole currency units).
func buildDocument(row dbgen.GetOrderForInvoiceRow, items []dbgen.ListInvoiceItemsRow, settings map[string]string) Document {
	var addr Address
	if len(row.ShippingAddress) > 0 {
		_ = json.Unmarshal(row.ShippingAddress, &addr)
	}
	doc := Document{
		OrderNumber: row.OrderNumber.String,
		OrderedAt:   row.CreatedAt.Time,
		Store: StoreInfo{
			Name:        firstNonEmpty(settings["storeName"], row.TenantName),
			Email:       firstNonEmpty(settings["contactEmail"], row.TenantContactEmail.String),
			Phone:       settings["contactPhone"],
			Description: settings["description"],
		},
		Customer:       Customer{Name: row.CustomerName.String, Email: row.CustomerEmail.String},
		Address:        addr,
		Currency:       row.Currency,
		Subtotal:       row.PricingSubtotal,
		Discount:       row.PricingDiscount,
		Tax:            row.PricingTax,
		Shipping:       row.PricingShipping,
		Total:          row.PricingTotal,
		VoucherCode:    row.AppliedVoucherCode.String,
		PaymentStatus:  row.PaymentStatus,
		PaymentChannel: row.PaymentChannel,
		OrderStatus:    string(row.Status),
	}
	if doc.OrderNumber == "" && row.ID.Valid {
		doc.OrderNumber = fmt.Sprintf("%x", row.ID.Bytes[:4])
	}
	for _, it := range items {
		doc.Items = append(doc.Items, Item{
			Title:     it.Title,
			SKU:       it.Sku.String,
			Qty:       it.Qty,
			UnitPrice: it.UnitPrice,
			Subtotal:  it.Subtotal,
		})
	}
	return doc
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
