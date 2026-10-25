package jobs

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/checkout"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
	"github.com/noah-isme/backend-toko/internal/tenant"
	fx "github.com/noah-isme/backend-toko/internal/testutil/pgfixture"
)

// Run with: TEST_DATABASE_URL=<migrated db> go test ./internal/jobs/ -run CheckoutExpiryIntegration

func newExpiryJob(d *fx.DB, tenants ...pgtype.UUID) *CheckoutExpiryJob {
	logger := zerolog.New(zerolog.NewTestWriter(d.T))
	return &CheckoutExpiryJob{
		Logger:      &logger,
		Pool:        d.Pool,
		Q:           d.Q,
		Events:      &events.Bus{Store: d.Q},
		FallbackTTL: 15 * time.Minute,
		Grace:       10 * time.Minute,
		SilentAfter: 24 * time.Hour,
		ProofHold:   72 * time.Hour,
		BatchSize:   100,
		Tenants: func(context.Context) ([]pgtype.UUID, error) {
			return tenants, nil
		},
	}
}

type expiryCase struct {
	d       *fx.DB
	tenant  pgtype.UUID
	user    pgtype.UUID
	email   string
	product pgtype.UUID
	variant pgtype.UUID
}

func newExpiryCase(t *testing.T) expiryCase {
	d := fx.Open(t)
	c := expiryCase{d: d, tenant: d.Tenant()}
	c.user, c.email = d.User()
	c.product, c.variant = d.Variant(c.tenant, 10)
	return c
}

func (c expiryCase) pending(paymentExpiresAt time.Time, flash bool) fx.Order {
	return c.d.Order(fx.OrderOpts{
		TenantID:         c.tenant,
		UserID:           c.user,
		CreatedAt:        paymentExpiresAt.Add(-15 * time.Minute),
		PaymentExpiresAt: paymentExpiresAt,
		Lines:            []fx.Line{{ProductID: c.product, VariantID: c.variant, Qty: 2, FlashSale: flash}},
	})
}

func TestCheckoutExpiryIntegration_ExpiresOrder(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	order := c.pending(time.Now().Add(-30*time.Minute), true)
	require.Equal(t, 8, d.Stock(c.variant))

	res, err := newExpiryJob(d, c.tenant).Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, res["orders_expired"])
	require.Equal(t, 1, res["payments_expired"])
	require.Equal(t, 1, res["reservations_released"])

	require.Equal(t, "EXPIRED", d.OrderStatus(order.ID))
	require.Equal(t, "EXPIRED", d.PaymentStatus(order.PaymentID))
	require.Equal(t, 1, d.Int(`SELECT count(*) FROM payment_events WHERE payment_id = $1 AND status = 'EXPIRED'`, order.PaymentID))
	require.Equal(t, "RELEASED", d.ReservationStatus(order.ReservationIDs[0]))
	require.Equal(t, 10, d.Stock(c.variant))
	require.Equal(t, "RELEASED", d.Str(`SELECT status FROM flash_sale_order_items WHERE order_id = $1`, order.ID))
	require.Equal(t, 0, d.Int(`SELECT sold_count FROM flash_sale_items WHERE id = $1`, order.FlashItemIDs[0]))

	require.Equal(t, 1, d.Events(order.ID, events.TopicPaymentExpired))
	require.Equal(t, 1, d.Events(order.ID, events.TopicOrderCanceled))
	var payload map[string]any
	require.NoError(t, json.Unmarshal([]byte(d.Str(`SELECT payload::text FROM domain_events WHERE aggregate_id = $1 AND topic = $2`, order.ID, events.TopicPaymentExpired)), &payload))
	require.Equal(t, "payment_deadline", payload["reason"])
	require.Equal(t, "worker", payload["source"])
	require.Equal(t, "EXPIRED", payload["status"])
	require.Equal(t, fx.UUIDString(c.tenant), payload["tenantId"])
	require.Equal(t, c.email, payload["email"])
}

func TestCheckoutExpiryIntegration_Idempotent(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	order := c.pending(time.Now().Add(-30*time.Minute), true)
	job := newExpiryJob(d, c.tenant)
	_, err := job.Run(context.Background())
	require.NoError(t, err)
	res, err := job.Run(context.Background())
	require.NoError(t, err)
	require.Zero(t, res["orders_expired"])
	require.Equal(t, 10, d.Stock(c.variant))
	require.Equal(t, 0, d.Int(`SELECT sold_count FROM flash_sale_items WHERE id = $1`, order.FlashItemIDs[0]))
	require.Equal(t, 1, d.Events(order.ID, events.TopicPaymentExpired))
}

func TestCheckoutExpiryIntegration_GracePeriodHoldsOrder(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	order := c.pending(time.Now().Add(-time.Minute), false)
	res, err := newExpiryJob(d, c.tenant).Run(context.Background())
	require.NoError(t, err)
	require.Zero(t, res["orders_expired"])
	require.Equal(t, "PENDING_PAYMENT", d.OrderStatus(order.ID))
	require.Equal(t, "RESERVED", d.ReservationStatus(order.ReservationIDs[0]))
	require.Equal(t, 8, d.Stock(c.variant))
}

func TestCheckoutExpiryIntegration_RenewedIntentKeepsReservation(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	order := d.Order(fx.OrderOpts{
		TenantID:             c.tenant,
		UserID:               c.user,
		CreatedAt:            time.Now().Add(-2 * time.Hour),
		PaymentExpiresAt:     time.Now().Add(10 * time.Minute),
		ReservationExpiresAt: time.Now().Add(-time.Hour),
		Lines:                []fx.Line{{ProductID: c.product, VariantID: c.variant, Qty: 2}},
	})
	_, err := newExpiryJob(d, c.tenant).Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, "PENDING_PAYMENT", d.OrderStatus(order.ID))
	require.Equal(t, "RESERVED", d.ReservationStatus(order.ReservationIDs[0]))
	require.Equal(t, 8, d.Stock(c.variant))
}

func TestCheckoutExpiryIntegration_SkipsPaidOrders(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	order := d.Order(fx.OrderOpts{
		TenantID:          c.tenant,
		UserID:            c.user,
		Status:            dbgen.OrderStatusPAID,
		PaymentExpiresAt:  time.Now().Add(-time.Hour),
		PaymentStatus:     dbgen.PaymentStatusPAID,
		ReservationStatus: dbgen.InventoryReservationStatusCOMMITTED,
		Lines:             []fx.Line{{ProductID: c.product, VariantID: c.variant, Qty: 2}},
	})
	_, err := newExpiryJob(d, c.tenant).Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, "PAID", d.OrderStatus(order.ID))
	require.Equal(t, "PAID", d.PaymentStatus(order.PaymentID))
	require.Zero(t, d.AllEvents(order.ID))
}

func TestCheckoutExpiryIntegration_FallbackDeadlineWithoutPayment(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	order := d.Order(fx.OrderOpts{
		TenantID:  c.tenant,
		UserID:    c.user,
		CreatedAt: time.Now().Add(-26 * time.Minute),
		Lines:     []fx.Line{{ProductID: c.product, VariantID: c.variant, Qty: 2}},
	})
	res, err := newExpiryJob(d, c.tenant).Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, res["orders_expired"])
	require.Equal(t, "EXPIRED", d.OrderStatus(order.ID))
	require.Equal(t, 10, d.Stock(c.variant))
}

func TestCheckoutExpiryIntegration_SilentBacklog(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	order := c.pending(time.Now().Add(-48*time.Hour), false)
	res, err := newExpiryJob(d, c.tenant).Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, res["orders_expired"])
	require.Equal(t, 1, res["orders_expired_silent"])
	require.Equal(t, "EXPIRED", d.OrderStatus(order.ID))
	require.Equal(t, 10, d.Stock(c.variant))
	require.Zero(t, d.AllEvents(order.ID))
	require.Equal(t, "true", d.Str(`SELECT payload->>'silent' FROM payment_events WHERE payment_id = $1 AND status = 'EXPIRED'`, order.PaymentID))
}

func TestCheckoutExpiryIntegration_PaymentProofHoldsOrder(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	held := c.pending(time.Now().Add(-2*time.Hour), false)
	d.Proof(held.ID, c.user, time.Now().Add(-time.Hour))

	// The hold runs from the first proof: re-uploading does not extend it.
	stale := c.pending(time.Now().Add(-90*time.Hour), false)
	d.Proof(stale.ID, c.user, time.Now().Add(-80*time.Hour))
	d.Proof(stale.ID, c.user, time.Now().Add(-time.Hour))

	job := newExpiryJob(d, c.tenant)
	res, err := job.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, "PENDING_PAYMENT", d.OrderStatus(held.ID))
	require.Equal(t, "EXPIRED", d.OrderStatus(stale.ID))
	require.Equal(t, 1, res["orders_expired"])

	// The Go re-check under the lock applies the same rule.
	outcome, err := job.expireOrder(tenant.WithTenant(context.Background(), fx.UUIDString(c.tenant)), c.tenant, held.ID)
	require.NoError(t, err)
	require.Equal(t, "skipped_proof_hold", outcome.counter)
}

func TestCheckoutExpiryIntegration_OrphanReservationReleased(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	order := d.Order(fx.OrderOpts{
		TenantID:             c.tenant,
		UserID:               c.user,
		Status:               dbgen.OrderStatusCANCELLED,
		ReservationExpiresAt: time.Now().Add(-time.Minute),
		Lines:                []fx.Line{{ProductID: c.product, VariantID: c.variant, Qty: 3, FlashSale: true}},
	})
	require.Equal(t, 7, d.Stock(c.variant))
	res, err := newExpiryJob(d, c.tenant).Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, res["reservations_expired"])
	require.Equal(t, "EXPIRED", d.ReservationStatus(order.ReservationIDs[0]))
	require.Equal(t, 10, d.Stock(c.variant))
	require.Equal(t, 0, d.Int(`SELECT sold_count FROM flash_sale_items WHERE id = $1`, order.FlashItemIDs[0]))
	require.Equal(t, "CANCELLED", d.OrderStatus(order.ID))
}

func TestCheckoutExpiryIntegration_AnomalousReservationUntouched(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	order := d.Order(fx.OrderOpts{
		TenantID:             c.tenant,
		UserID:               c.user,
		Status:               dbgen.OrderStatusPAID,
		ReservationExpiresAt: time.Now().Add(-time.Minute),
		Lines:                []fx.Line{{ProductID: c.product, VariantID: c.variant, Qty: 1}},
	})
	res, err := newExpiryJob(d, c.tenant).Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, res["reservations_anomalous"])
	require.Equal(t, "RESERVED", d.ReservationStatus(order.ReservationIDs[0]))
	require.Equal(t, 9, d.Stock(c.variant))
}

func TestCheckoutExpiryIntegration_BatchesAcrossTenants(t *testing.T) {
	d := fx.Open(t)
	tenantA, tenantB := d.Tenant(), d.Tenant()
	user, _ := d.User()
	var orders []fx.Order
	for i, ten := range []pgtype.UUID{tenantA, tenantA, tenantA, tenantB, tenantB} {
		product, variant := d.Variant(ten, 5)
		orders = append(orders, d.Order(fx.OrderOpts{
			TenantID:         ten,
			UserID:           user,
			CreatedAt:        time.Now().Add(-time.Hour - time.Duration(i)*time.Minute),
			PaymentExpiresAt: time.Now().Add(-45 * time.Minute),
			Lines:            []fx.Line{{ProductID: product, VariantID: variant, Qty: 1}},
		}))
	}
	job := newExpiryJob(d, tenantA, tenantB)
	job.BatchSize = 2
	res, err := job.Run(context.Background())
	require.NoError(t, err)
	require.Equal(t, 5, res["orders_expired"])
	require.Equal(t, 2, res["tenants"])
	for _, o := range orders {
		require.Equal(t, "EXPIRED", d.OrderStatus(o.ID))
	}
}

func TestCheckoutExpiryIntegration_SkipsRowLockedByAnotherTx(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	locked := c.pending(time.Now().Add(-30*time.Minute), false)
	free := c.pending(time.Now().Add(-31*time.Minute), false)

	ctx := context.Background()
	tx, err := d.Pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	_, err = tx.Exec(ctx, `SELECT id FROM orders WHERE id = $1 FOR UPDATE`, locked.ID)
	require.NoError(t, err)

	job := newExpiryJob(d, c.tenant)
	res, err := job.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, res["skipped_locked_or_changed"])
	require.Equal(t, "PENDING_PAYMENT", d.OrderStatus(locked.ID))
	require.Equal(t, "EXPIRED", d.OrderStatus(free.ID))

	require.NoError(t, tx.Rollback(ctx))
	res, err = job.Run(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, res["orders_expired"])
	require.Equal(t, "EXPIRED", d.OrderStatus(locked.ID))
	require.Equal(t, 10, d.Stock(c.variant))
}

func TestCheckoutExpiryIntegration_ExpiredOrderExcludedFromRevenue(t *testing.T) {
	c := newExpiryCase(t)
	d := c.d
	c.pending(time.Now().Add(-30*time.Minute), false)
	_, err := newExpiryJob(d, c.tenant).Run(context.Background())
	require.NoError(t, err)

	stats, err := d.Q.AdminOrderStats(context.Background(), dbgen.AdminOrderStatsParams{TenantID: c.tenant})
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.ExpiredOrders)
	require.Equal(t, int64(0), stats.CancelledOrders)
	top, err := d.Q.AdminTopProductsByRevenue(context.Background(), dbgen.AdminTopProductsByRevenueParams{TenantID: c.tenant, LimitValue: 10})
	require.NoError(t, err)
	require.Empty(t, top)
}

func TestCheckoutExpiryIntegration_ListsActiveTenants(t *testing.T) {
	d := fx.Open(t)
	active := d.Tenant()
	inactive := d.Tenant()
	_, err := d.Pool.Exec(context.Background(), `UPDATE tenants SET status = 'suspended' WHERE id = $1`, inactive)
	require.NoError(t, err)
	ids, err := d.Q.ListActiveTenantIDs(context.Background())
	require.NoError(t, err)
	require.Contains(t, ids, active)
	require.NotContains(t, ids, inactive)
}

// Checkout reserves variants in ascending variant_id, as does every release
// path, so a checkout racing the expiry of an order that holds the same
// variants must never fail with a deadlock. The cart lists the variants in
// the opposite order by title to exercise the sort.
func TestCheckoutExpiryIntegration_ConcurrentCheckoutAndExpiry(t *testing.T) {
	d := fx.Open(t)
	ten := d.Tenant()
	ctx := tenant.WithTenant(context.Background(), fx.UUIDString(ten))
	svc := &checkout.Service{Q: d.Q, Pool: d.Pool, Currency: "IDR"}
	job := newExpiryJob(d, ten)

	const rounds = 15
	for i := 0; i < rounds; i++ {
		p1, v1 := d.Variant(ten, 10)
		p2, v2 := d.Variant(ten, 10)
		buyer, _ := d.User()
		// Title order is the reverse of variant order.
		first, second := fx.CartLine{ProductID: p1, VariantID: v1, Qty: 1}, fx.CartLine{ProductID: p2, VariantID: v2, Qty: 1}
		first.Title, second.Title = "b", "a"
		cartID, addrID := d.Cart(ten, buyer, first, second)
		old := d.Order(fx.OrderOpts{
			TenantID:         ten,
			UserID:           buyer,
			CreatedAt:        time.Now().Add(-time.Hour),
			PaymentExpiresAt: time.Now().Add(-40 * time.Minute),
			Lines:            []fx.Line{{ProductID: p1, VariantID: v1, Qty: 2}, {ProductID: p2, VariantID: v2, Qty: 2}},
		})

		var wg sync.WaitGroup
		var checkoutErr, expiryErr error
		var outcome expiryOutcome
		wg.Add(2)
		go func() {
			defer wg.Done()
			uid := fx.UUIDString(buyer)
			_, checkoutErr = svc.Create(ctx, &uid, checkout.Input{
				CartID:            fx.UUIDString(cartID),
				ShippingAddressID: fx.UUIDString(addrID),
				ShippingService:   "jne-reg",
				PaymentMethod:     "bank_transfer",
			})
		}()
		go func() {
			defer wg.Done()
			outcome, expiryErr = job.expireOrder(ctx, ten, old.ID)
		}()
		wg.Wait()
		require.NoError(t, checkoutErr, "round %d", i)
		require.NoError(t, expiryErr, "round %d", i)
		require.Equal(t, "orders_expired", outcome.counter, "round %d", i)
		// 10 initial - 2 (old order, released) - 1 (new checkout) = 9.
		require.Equal(t, 9, d.Stock(v1), "round %d", i)
		require.Equal(t, 9, d.Stock(v2), "round %d", i)
	}
}
