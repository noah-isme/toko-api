// Package pgfixture seeds orders, payments, reservations and flash-sale rows
// for Postgres-backed integration tests. It is imported only from _test.go
// files. Tests using it are opt-in: they skip unless TEST_DATABASE_URL points
// at a migrated database.
package pgfixture

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// DB is a connection to the test database plus cleanup bookkeeping.
type DB struct {
	T       testing.TB
	Pool    *pgxpool.Pool
	Q       *dbgen.Queries
	URL     string
	tenants []string
	users   []string
}

// Open connects to TEST_DATABASE_URL or skips the test. Everything created
// through the returned DB is deleted when the test ends.
func Open(t testing.TB) *DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping test database: %v", err)
	}
	d := &DB{T: t, Pool: pool, Q: dbgen.New(pool), URL: url}
	t.Cleanup(func() {
		cctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM domain_events WHERE aggregate_id IN (SELECT id FROM orders WHERE tenant_id = ANY($1::uuid[]))`,
			`DELETE FROM domain_events WHERE aggregate_id IN (SELECT s.id FROM shipments s WHERE s.tenant_id = ANY($1::uuid[]))`,
			`DELETE FROM notifications WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM orders WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM carts WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM flash_sale_campaigns WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM products WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM webhook_endpoints WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM tenants WHERE id = ANY($1::uuid[])`,
		} {
			if _, err := pool.Exec(cctx, stmt, d.tenants); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		if _, err := pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1::uuid[])`, d.users); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
		pool.Close()
	})
	return d
}

// RandSuffix returns a short random hex string.
func RandSuffix() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (d *DB) must(err error, what string) {
	d.T.Helper()
	if err != nil {
		d.T.Fatalf("%s: %v", what, err)
	}
}

// Tenant inserts an active tenant.
func (d *DB) Tenant() pgtype.UUID {
	d.T.Helper()
	var id pgtype.UUID
	d.must(d.Pool.QueryRow(context.Background(), `INSERT INTO tenants(slug, name) VALUES ($1, 'IT tenant') RETURNING id`, "it-"+RandSuffix()).Scan(&id), "insert tenant")
	d.tenants = append(d.tenants, UUIDString(id))
	return id
}

// User inserts a customer and returns its id and email.
func (d *DB) User() (pgtype.UUID, string) {
	d.T.Helper()
	email := "it-" + RandSuffix() + "@example.test"
	var id pgtype.UUID
	d.must(d.Pool.QueryRow(context.Background(), `INSERT INTO users(name, email, password_hash) VALUES ('IT user', $1, 'x') RETURNING id`, email).Scan(&id), "insert user")
	d.users = append(d.users, UUIDString(id))
	return id, email
}

// Variant inserts a product with one variant holding `stock` units.
func (d *DB) Variant(tenantID pgtype.UUID, stock int) (productID, variantID pgtype.UUID) {
	d.T.Helper()
	ctx := context.Background()
	d.must(d.Pool.QueryRow(ctx, `INSERT INTO products(title, slug, price, tenant_id) VALUES ('IT product', $1, 10000, $2) RETURNING id`, "it-prod-"+RandSuffix(), tenantID).Scan(&productID), "insert product")
	d.must(d.Pool.QueryRow(ctx, `INSERT INTO product_variants(product_id, price, stock) VALUES ($1, 10000, $2) RETURNING id`, productID, stock).Scan(&variantID), "insert variant")
	return productID, variantID
}

// Line is one reserved order line.
type Line struct {
	ProductID pgtype.UUID
	VariantID pgtype.UUID
	Qty       int
	// FlashSale reserves the quantity on a new active flash-sale item too.
	FlashSale bool
}

// OrderOpts describes an order to seed. Zero values mean: PENDING_PAYMENT,
// created now, no payment row, reservations RESERVED and expiring now.
type OrderOpts struct {
	TenantID  pgtype.UUID
	UserID    pgtype.UUID
	Status    dbgen.OrderStatus
	CreatedAt time.Time
	Lines     []Line
	// PaymentExpiresAt creates a payment row when non-zero.
	PaymentExpiresAt     time.Time
	PaymentStatus        dbgen.PaymentStatus
	ReservationExpiresAt time.Time
	ReservationStatus    dbgen.InventoryReservationStatus
	Total                int64
	VoucherCode          string
}

// Order is a seeded order.
type Order struct {
	ID             pgtype.UUID
	PaymentID      pgtype.UUID
	ReservationIDs []pgtype.UUID
	FlashItemIDs   []pgtype.UUID
}

// Order seeds an order. Stock is taken from each line's variant (and from the
// flash-sale item's sold_count) only when the reservation status is RESERVED,
// mirroring checkout.
func (d *DB) Order(o OrderOpts) Order {
	d.T.Helper()
	ctx := context.Background()
	now := time.Now()
	if o.Status == "" {
		o.Status = dbgen.OrderStatusPENDINGPAYMENT
	}
	if o.CreatedAt.IsZero() {
		o.CreatedAt = now
	}
	if o.ReservationExpiresAt.IsZero() {
		o.ReservationExpiresAt = now
	}
	if o.ReservationStatus == "" {
		o.ReservationStatus = dbgen.InventoryReservationStatusRESERVED
	}
	if o.Total == 0 {
		o.Total = 10000
	}
	var out Order
	d.must(d.Pool.QueryRow(ctx, `INSERT INTO orders(user_id, status, pricing_subtotal, pricing_total, tenant_id, order_number, created_at, updated_at, applied_voucher_code)
		VALUES ($1, $2, $3, $3, $4, $5, $6, $6, NULLIF($7, '')) RETURNING id`,
		o.UserID, o.Status, o.Total, o.TenantID, "IT-"+RandSuffix(), o.CreatedAt, o.VoucherCode).Scan(&out.ID), "insert order")
	if !o.PaymentExpiresAt.IsZero() {
		status := o.PaymentStatus
		if status == "" {
			status = dbgen.PaymentStatusPENDING
		}
		d.must(d.Pool.QueryRow(ctx, `INSERT INTO payments(order_id, provider, channel, status, amount, expires_at, tenant_id, created_at, updated_at)
			VALUES ($1, 'stub', 'bank_transfer', $2, $3, $4, $5, $6, $6) RETURNING id`,
			out.ID, status, o.Total, o.PaymentExpiresAt, o.TenantID, o.CreatedAt).Scan(&out.PaymentID), "insert payment")
	}
	for _, line := range o.Lines {
		var resID pgtype.UUID
		d.must(d.Pool.QueryRow(ctx, `INSERT INTO inventory_reservations(tenant_id, order_id, product_id, variant_id, qty, status, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
			o.TenantID, out.ID, line.ProductID, line.VariantID, line.Qty, o.ReservationStatus, o.ReservationExpiresAt).Scan(&resID), "insert reservation")
		out.ReservationIDs = append(out.ReservationIDs, resID)
		if _, err := d.Pool.Exec(ctx, `INSERT INTO order_items(order_id, product_id, variant_id, title, slug, qty, unit_price, subtotal)
			VALUES ($1, $2, $3, 'IT product', 'it-product', $4, 10000, $5)`, out.ID, line.ProductID, line.VariantID, line.Qty, int64(line.Qty)*10000); err != nil {
			d.T.Fatalf("insert order item: %v", err)
		}
		reserved := o.ReservationStatus == dbgen.InventoryReservationStatusRESERVED
		if reserved {
			_, err := d.Pool.Exec(ctx, `UPDATE product_variants SET stock = stock - $2 WHERE id = $1`, line.VariantID, line.Qty)
			d.must(err, "reserve stock")
		}
		if line.FlashSale {
			var campaignID, itemID pgtype.UUID
			d.must(d.Pool.QueryRow(ctx, `INSERT INTO flash_sale_campaigns(tenant_id, name, slug, status, starts_at, ends_at)
				VALUES ($1, 'IT sale', $2, 'ACTIVE', now() - interval '1 day', now() + interval '1 day') RETURNING id`,
				o.TenantID, "it-sale-"+RandSuffix()).Scan(&campaignID), "insert campaign")
			sold := 0
			fsStatus := "COMMITTED"
			if reserved {
				sold = line.Qty
				fsStatus = "RESERVED"
			}
			d.must(d.Pool.QueryRow(ctx, `INSERT INTO flash_sale_items(campaign_id, product_id, sale_price, stock_limit, sold_count)
				VALUES ($1, $2, 5000, 100, $3) RETURNING id`, campaignID, line.ProductID, sold).Scan(&itemID), "insert flash item")
			_, err := d.Pool.Exec(ctx, `INSERT INTO flash_sale_order_items(order_id, flash_sale_item_id, qty, status) VALUES ($1, $2, $3, $4)`,
				out.ID, itemID, line.Qty, fsStatus)
			d.must(err, "insert flash order item")
			out.FlashItemIDs = append(out.FlashItemIDs, itemID)
		}
	}
	return out
}

// CartLine is one cart item.
type CartLine struct {
	ProductID pgtype.UUID
	VariantID pgtype.UUID
	Title     string
	Qty       int
}

// Cart inserts a cart with items for the user, plus a shipping address, and
// returns both ids (ready for checkout.Service.Create).
func (d *DB) Cart(tenantID, userID pgtype.UUID, lines ...CartLine) (cartID, addressID pgtype.UUID) {
	d.T.Helper()
	ctx := context.Background()
	d.must(d.Pool.QueryRow(ctx, `INSERT INTO carts(user_id, tenant_id) VALUES ($1, $2) RETURNING id`, userID, tenantID).Scan(&cartID), "insert cart")
	for _, l := range lines {
		_, err := d.Pool.Exec(ctx, `INSERT INTO cart_items(cart_id, product_id, variant_id, title, slug, qty, unit_price, subtotal)
			VALUES ($1, $2, $3, $4, 'it-product', $5, 10000, $6)`, cartID, l.ProductID, l.VariantID, l.Title, l.Qty, int64(l.Qty)*10000)
		d.must(err, "insert cart item")
	}
	d.must(d.Pool.QueryRow(ctx, `INSERT INTO addresses(user_id, receiver_name, phone, country, province, city, postal_code, address_line1)
		VALUES ($1, 'IT', '0800', 'ID', 'DKI', 'Jakarta', '10110', 'Jl. Test 1') RETURNING id`, userID).Scan(&addressID), "insert address")
	return cartID, addressID
}

// Proof records a payment proof uploaded at createdAt.
func (d *DB) Proof(orderID, userID pgtype.UUID, createdAt time.Time) {
	d.T.Helper()
	_, err := d.Pool.Exec(context.Background(), `INSERT INTO payment_proofs(order_id, user_id, filename, content_type, content, created_at)
		VALUES ($1, $2, 'proof.png', 'image/png', '\x00'::bytea, $3)`, orderID, userID, createdAt)
	d.must(err, "insert payment proof")
}

// Int runs a query returning one integer.
func (d *DB) Int(sql string, args ...any) int {
	d.T.Helper()
	var n int
	d.must(d.Pool.QueryRow(context.Background(), sql, args...).Scan(&n), sql)
	return n
}

// Str runs a query returning one text value.
func (d *DB) Str(sql string, args ...any) string {
	d.T.Helper()
	var s string
	d.must(d.Pool.QueryRow(context.Background(), sql, args...).Scan(&s), sql)
	return s
}

// Stock returns a variant's stock.
func (d *DB) Stock(variantID pgtype.UUID) int {
	return d.Int(`SELECT stock FROM product_variants WHERE id = $1`, variantID)
}

// OrderStatus returns an order's status.
func (d *DB) OrderStatus(orderID pgtype.UUID) string {
	return d.Str(`SELECT status::text FROM orders WHERE id = $1`, orderID)
}

// PaymentStatus returns a payment's status.
func (d *DB) PaymentStatus(paymentID pgtype.UUID) string {
	return d.Str(`SELECT status::text FROM payments WHERE id = $1`, paymentID)
}

// ReservationStatus returns a reservation's status.
func (d *DB) ReservationStatus(id pgtype.UUID) string {
	return d.Str(`SELECT status::text FROM inventory_reservations WHERE id = $1`, id)
}

// Events counts domain events for an aggregate and topic.
func (d *DB) Events(aggregateID pgtype.UUID, topic string) int {
	return d.Int(`SELECT count(*) FROM domain_events WHERE aggregate_id = $1 AND topic = $2`, aggregateID, topic)
}

// AllEvents counts every domain event for an aggregate.
func (d *DB) AllEvents(aggregateID pgtype.UUID) int {
	return d.Int(`SELECT count(*) FROM domain_events WHERE aggregate_id = $1`, aggregateID)
}

// UUIDString formats a pgtype.UUID.
func UUIDString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	b := id.Bytes
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 36)
	j := 0
	for i, v := range b {
		if i == 4 || i == 6 || i == 8 || i == 10 {
			out[j] = '-'
			j++
		}
		out[j] = hexdigits[v>>4]
		out[j+1] = hexdigits[v&0x0f]
		j += 2
	}
	return string(out)
}
