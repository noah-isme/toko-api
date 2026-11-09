package admin_test

import (
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/admin"
	"github.com/noah-isme/backend-toko/internal/analytics"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// These tests drive the analytics, admin overview/order-stats and report
// handlers against a real Postgres with all migrations applied. They are the
// proof of tenant isolation and revenue consistency. Opt-in: set
// TEST_DATABASE_URL to a migrated database.
//
// Tenant A (Asia/Jakarta). D1 = 2026-10-01 WIB, D2 = 2026-10-02 WIB; the
// "both days" range is from=2026-10-01&to=2026-10-03, i.e.
// [2026-09-30T17:00:00Z, 2026-10-02T17:00:00Z).
//
//	order user  created_at (UTC)       day status           total   items         refunds
//	A1    U1    2026-09-30T20:00:00Z   D1  PAID             100000  P1x1 100000   PENDING 10000 (subtracted)
//	A2    U2    2026-10-01T06:00:00Z   D1  PAID             200000  P1x2 200000   SUCCEEDED 5000 (partial)
//	A3    guest 2026-10-01T04:00:00Z   D1  PENDING_PAYMENT   70000  P1x1  70000
//	A4    U1    2026-10-02T05:00:00Z   D2  DELIVERED        150000  P2x1 150000   FAILED 150000 (ignored)
//	A5    guest 2026-10-02T06:00:00Z   D2  SHIPPED           50000  P2x1  50000
//	A6    U3    2026-10-02T07:00:00Z   D2  DELIVERED        999000  P1x1 999000   SUCCEEDED 999000 (full)
//	A7    U2    2026-10-02T08:00:00Z   D2  CANCELLED         80000  P2x1  80000
//
// Net order revenue: A1 90000 + A2 195000 + A4 150000 + A5 50000 = 485000.
// Item-level (gross subtotal of not-fully-refunded revenue orders): 500000.
//
// Tenant B (UTC) is the leak decoy: U1's earlier B1 (09-15), U1's in-range B2,
// U2's fully refunded B3, two carts on D1, and its own category/product.

type rtest struct {
	t      *testing.T
	pool   *pgxpool.Pool
	router http.Handler
	tenA   string
	tenB   string
	users  []string
	c1, p1 string
	p2     string
}

func rslug(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func newRTest(t *testing.T) *rtest {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(ctx))

	e := &rtest{t: t, pool: pool}
	e.tenA = e.scalar(`INSERT INTO tenants(slug,name,timezone) VALUES($1,'Report A','Asia/Jakarta') RETURNING id::text`, rslug("rep-a"))
	e.tenB = e.scalar(`INSERT INTO tenants(slug,name) VALUES($1,'Report B') RETURNING id::text`, rslug("rep-b"))
	for i := 0; i < 3; i++ {
		e.users = append(e.users, e.scalar(`INSERT INTO users(name,email,password_hash) VALUES('Report user',$1,'x') RETURNING id::text`, rslug("rep-user")+"@example.test"))
	}
	t.Cleanup(func() {
		cctx := context.Background()
		tenants := []string{e.tenA, e.tenB}
		for _, stmt := range []string{
			`DELETE FROM orders WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM carts WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM products WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM categories WHERE tenant_id = ANY($1::uuid[])`,
			`DELETE FROM tenants WHERE id = ANY($1::uuid[])`,
		} {
			if _, err := pool.Exec(cctx, stmt, tenants); err != nil {
				t.Errorf("cleanup %q: %v", stmt, err)
			}
		}
		if _, err := pool.Exec(cctx, `DELETE FROM users WHERE id = ANY($1::uuid[])`, e.users); err != nil {
			t.Errorf("cleanup users: %v", err)
		}
	})
	u1, u2, u3 := e.users[0], e.users[1], e.users[2]

	// Tenant A catalog and orders.
	e.c1 = e.scalar(`INSERT INTO categories(name,slug,tenant_id) VALUES('Laptop',$1,$2) RETURNING id::text`, rslug("laptop"), e.tenA)
	e.p1 = e.scalar(`INSERT INTO products(title,slug,price,tenant_id,category_id) VALUES('Laptop Pro',$1,100000,$2,$3) RETURNING id::text`, rslug("p1"), e.tenA, e.c1)
	e.p2 = e.scalar(`INSERT INTO products(title,slug,price,tenant_id) VALUES('Cable',$1,50000,$2) RETURNING id::text`, rslug("p2"), e.tenA)
	a1 := e.order(e.tenA, u1, "2026-09-30T20:00:00Z", "PAID", 100000, e.p1, 1)
	a2 := e.order(e.tenA, u2, "2026-10-01T06:00:00Z", "PAID", 200000, e.p1, 2)
	e.order(e.tenA, "", "2026-10-01T04:00:00Z", "PENDING_PAYMENT", 70000, e.p1, 1)
	a4 := e.order(e.tenA, u1, "2026-10-02T05:00:00Z", "DELIVERED", 150000, e.p2, 1)
	e.order(e.tenA, "", "2026-10-02T06:00:00Z", "SHIPPED", 50000, e.p2, 1)
	a6 := e.order(e.tenA, u3, "2026-10-02T07:00:00Z", "DELIVERED", 999000, e.p1, 1)
	e.order(e.tenA, u2, "2026-10-02T08:00:00Z", "CANCELLED", 80000, e.p2, 1)
	e.refund(e.tenA, a1, u1, 10000, "PENDING")
	e.refund(e.tenA, a2, u2, 5000, "SUCCEEDED")
	e.refund(e.tenA, a4, u1, 150000, "FAILED")
	e.refund(e.tenA, a6, u3, 999000, "SUCCEEDED")
	for _, at := range []string{"2026-10-01T02:00:00Z", "2026-10-01T09:00:00Z", "2026-10-02T03:00:00Z"} {
		e.exec(`INSERT INTO carts(tenant_id,created_at) VALUES($1,$2)`, e.tenA, ts(at))
	}

	// Tenant B decoy.
	cb := e.scalar(`INSERT INTO categories(name,slug,tenant_id) VALUES('Decoy',$1,$2) RETURNING id::text`, rslug("decoy"), e.tenB)
	pb := e.scalar(`INSERT INTO products(title,slug,price,tenant_id,category_id) VALUES('Decoy',$1,1000000,$2,$3) RETURNING id::text`, rslug("pb"), e.tenB, cb)
	e.order(e.tenB, u1, "2026-09-15T10:00:00Z", "PAID", 5000000, pb, 5)
	e.order(e.tenB, u1, "2026-10-01T10:00:00Z", "PAID", 3000000, pb, 3)
	b3 := e.order(e.tenB, u2, "2026-10-01T11:00:00Z", "DELIVERED", 777000, pb, 1)
	e.refund(e.tenB, b3, u2, 777000, "SUCCEEDED")
	for _, at := range []string{"2026-10-01T01:00:00Z", "2026-10-01T05:00:00Z"} {
		e.exec(`INSERT INTO carts(tenant_id,created_at) VALUES($1,$2)`, e.tenB, ts(at))
	}

	q := dbgen.New(pool)
	analyticsHandler := &analytics.Handler{Svc: &analytics.Service{Q: q}}
	overview := &admin.AnalyticsHandler{Q: q}
	orders := &admin.OrdersHandler{Q: q}
	reports := &admin.ReportsHandler{Q: q}
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if id := req.Header.Get("X-Tenant-ID"); id != "" {
				req = req.WithContext(tenant.WithTenant(req.Context(), id))
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Get("/api/v1/analytics/sales", analyticsHandler.Sales)
	r.Get("/api/v1/analytics/top-products", analyticsHandler.TopProducts)
	r.Get("/api/v1/analytics/overview", analyticsHandler.Overview)
	r.Get("/api/v1/admin/analytics/overview", overview.Overview)
	r.Get("/api/v1/admin/orders/stats", orders.OrderStats)
	r.Get("/api/v1/admin/reports/sales-by-category", reports.SalesByCategory)
	r.Get("/api/v1/admin/reports/sales-by-product", reports.SalesByProduct)
	r.Get("/api/v1/admin/reports/conversion", reports.Conversion)
	r.Get("/api/v1/admin/reports/customers", reports.Customers)
	e.router = r
	return e
}

func (e *rtest) scalar(sql string, args ...any) string {
	e.t.Helper()
	var out string
	require.NoError(e.t, e.pool.QueryRow(context.Background(), sql, args...).Scan(&out))
	return out
}

func (e *rtest) exec(sql string, args ...any) {
	e.t.Helper()
	_, err := e.pool.Exec(context.Background(), sql, args...)
	require.NoError(e.t, err)
}

func (e *rtest) order(tenantID, userID, createdAt, status string, total int64, productID string, qty int) string {
	e.t.Helper()
	var user any
	if userID != "" {
		user = userID
	}
	id := e.scalar(`INSERT INTO orders(tenant_id,user_id,status,pricing_subtotal,pricing_total,created_at)
		VALUES($1,$2,$3::order_status,$4,$4,$5) RETURNING id::text`, tenantID, user, status, total, ts(createdAt))
	e.exec(`INSERT INTO order_items(order_id,product_id,title,slug,qty,unit_price,subtotal) VALUES($1,$2,'Item','item',$3,$4,$5)`,
		id, productID, qty, total/int64(qty), total)
	return id
}

func (e *rtest) refund(tenantID, orderID, userID string, amount int64, status string) {
	e.t.Helper()
	returnStatus := "REFUNDED"
	if status == "FAILED" {
		returnStatus = "REJECTED"
	}
	returnID := e.scalar(`INSERT INTO returns(tenant_id,order_id,user_id,status,reason) VALUES($1,$2,$3,$4,'test') RETURNING id::text`, tenantID, orderID, userID, returnStatus)
	e.exec(`INSERT INTO refunds(tenant_id,return_id,order_id,provider,provider_ref,amount,status) VALUES($1,$2,$3,'test',$4,$5,$6)`,
		tenantID, returnID, orderID, rslug("ref"), amount, status)
}

func (e *rtest) get(tenantID, path string) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Tenant-ID", tenantID)
	rec := httptest.NewRecorder()
	e.router.ServeHTTP(rec, req)
	require.Equal(e.t, http.StatusOK, rec.Code, "%s: %s", path, rec.Body.String())
	return rec
}

func (e *rtest) getJSON(tenantID, path string, out any) {
	e.t.Helper()
	rec := e.get(tenantID, path)
	require.NoError(e.t, json.Unmarshal(rec.Body.Bytes(), out), rec.Body.String())
}

type salesDay struct {
	Day        string `json:"day"`
	PaidOrders int64  `json:"paid_orders"`
	AllOrders  int64  `json:"all_orders"`
	Revenue    int64  `json:"revenue"`
}

type categoryRow struct {
	CategoryID   *string `json:"categoryId"`
	CategoryName string  `json:"categoryName"`
	UnitsSold    int64   `json:"unitsSold"`
	Orders       int64   `json:"orders"`
	Revenue      int64   `json:"revenue"`
	Share        float64 `json:"share"`
}

type categoryReport struct {
	Data struct {
		Timezone     string        `json:"timezone"`
		TotalRevenue int64         `json:"totalRevenue"`
		TotalUnits   int64         `json:"totalUnits"`
		Rows         []categoryRow `json:"rows"`
	} `json:"data"`
}

type productReport struct {
	Data []struct {
		ProductID  *string `json:"productId"`
		CategoryID *string `json:"categoryId"`
		UnitsSold  int64   `json:"unitsSold"`
		Revenue    int64   `json:"revenue"`
	} `json:"data"`
	Pagination struct {
		TotalItems int `json:"totalItems"`
	} `json:"pagination"`
}

type conversionReport struct {
	Data struct {
		CartsCreated     int64   `json:"cartsCreated"`
		CheckoutsStarted int64   `json:"checkoutsStarted"`
		PaidOrders       int64   `json:"paidOrders"`
		PendingOrders    int64   `json:"pendingOrders"`
		CancelledOrders  int64   `json:"cancelledOrders"`
		RefundedOrders   int64   `json:"refundedOrders"`
		PaidRevenue      int64   `json:"paidRevenue"`
		RefundedAmount   int64   `json:"refundedAmount"`
		CheckoutRate     float64 `json:"checkoutRate"`
		PaymentRate      float64 `json:"paymentRate"`
		ConversionRate   float64 `json:"conversionRate"`
	} `json:"data"`
}

type customersReport struct {
	Data struct {
		Timezone             string  `json:"timezone"`
		NewCustomers         int64   `json:"newCustomers"`
		ReturningCustomers   int64   `json:"returningCustomers"`
		TotalCustomers       int64   `json:"totalCustomers"`
		RepeatRate           float64 `json:"repeatRate"`
		OrdersFromNew        int64   `json:"ordersFromNew"`
		OrdersFromReturning  int64   `json:"ordersFromReturning"`
		RevenueFromNew       int64   `json:"revenueFromNew"`
		RevenueFromReturning int64   `json:"revenueFromReturning"`
		GuestOrders          int64   `json:"guestOrders"`
		GuestRevenue         int64   `json:"guestRevenue"`
		Daily                []struct {
			Day                string `json:"day"`
			NewCustomers       int64  `json:"newCustomers"`
			ReturningCustomers int64  `json:"returningCustomers"`
			Orders             int64  `json:"orders"`
			Revenue            int64  `json:"revenue"`
		} `json:"daily"`
	} `json:"data"`
}

type statsBody struct {
	Data struct {
		TotalRevenue      int64 `json:"totalRevenue"`
		RevenueOrders     int64 `json:"revenueOrders"`
		TotalOrders       int64 `json:"totalOrders"`
		AverageOrderValue int64 `json:"averageOrderValue"`
		TopProducts       []struct {
			ProductID string `json:"productId"`
			Revenue   int64  `json:"revenue"`
		} `json:"topProducts"`
	} `json:"data"`
}

const bothDays = "from=2026-10-01&to=2026-10-03"

func TestReportsIntegrationTenantA(t *testing.T) {
	e := newRTest(t)
	a := e.tenA

	// Legacy sales: tenant-local buckets, net revenue, A1 crosses UTC midnight.
	var sales struct{ Data []salesDay }
	e.getJSON(a, "/api/v1/analytics/sales?from=2026-09-30T17:00:00Z&to=2026-10-02T17:00:00Z", &sales)
	require.Equal(t, []salesDay{
		{Day: "2026-10-01T00:00:00+07:00", PaidOrders: 2, AllOrders: 3, Revenue: 285000},
		{Day: "2026-10-02T00:00:00+07:00", PaidOrders: 2, AllOrders: 4, Revenue: 200000},
	}, sales.Data)
	var salesSum int64
	for _, d := range sales.Data {
		salesSum += d.Revenue
	}

	// Admin overview (all time) and order stats (explicit range).
	var ov statsBody
	e.getJSON(a, "/api/v1/admin/analytics/overview?range=all", &ov)
	require.Equal(t, int64(485000), ov.Data.TotalRevenue)
	require.Equal(t, int64(4), ov.Data.RevenueOrders)
	require.Equal(t, int64(121250), ov.Data.AverageOrderValue)
	require.Equal(t, int64(7), ov.Data.TotalOrders)
	require.Len(t, ov.Data.TopProducts, 2)
	require.Equal(t, e.p1, ov.Data.TopProducts[0].ProductID)
	require.Equal(t, int64(300000), ov.Data.TopProducts[0].Revenue, "A6 (fully refunded) must not count")
	require.Equal(t, int64(200000), ov.Data.TopProducts[1].Revenue)

	var stats statsBody
	e.getJSON(a, "/api/v1/admin/orders/stats?startDate=2026-09-30T17:00:00Z&endDate=2026-10-02T17:00:00Z", &stats)
	require.Equal(t, int64(485000), stats.Data.TotalRevenue)
	require.Equal(t, int64(4), stats.Data.RevenueOrders)
	require.Equal(t, int64(121250), stats.Data.AverageOrderValue)
	require.Equal(t, int64(7), stats.Data.TotalOrders)
	// endDate is honored: only D1 orders.
	e.getJSON(a, "/api/v1/admin/orders/stats?startDate=2026-09-30T17:00:00Z&endDate=2026-10-01T17:00:00Z", &stats)
	require.Equal(t, int64(285000), stats.Data.TotalRevenue)
	require.Equal(t, int64(3), stats.Data.TotalOrders)

	// Conversion.
	var conv conversionReport
	e.getJSON(a, "/api/v1/admin/reports/conversion?"+bothDays, &conv)
	c := conv.Data
	require.Equal(t, int64(3), c.CartsCreated)
	require.Equal(t, int64(7), c.CheckoutsStarted)
	require.Equal(t, int64(4), c.PaidOrders)
	require.Equal(t, int64(1), c.PendingOrders)
	require.Equal(t, int64(1), c.CancelledOrders)
	require.Equal(t, int64(1), c.RefundedOrders)
	require.Equal(t, int64(485000), c.PaidRevenue)
	require.Equal(t, int64(1014000), c.RefundedAmount, "PENDING+SUCCEEDED refunds, FAILED excluded")
	require.Equal(t, 2.3333, c.CheckoutRate)
	require.Equal(t, 0.5714, c.PaymentRate)
	require.Equal(t, 1.3333, c.ConversionRate)
	require.Equal(t, c.CheckoutsStarted, c.PaidOrders+c.PendingOrders+c.CancelledOrders+c.RefundedOrders)

	// Customers.
	var cust customersReport
	e.getJSON(a, "/api/v1/admin/reports/customers?"+bothDays, &cust)
	s := cust.Data
	require.Equal(t, "Asia/Jakarta", s.Timezone)
	require.Equal(t, int64(2), s.NewCustomers, "U1 must stay new at A despite U1's earlier order at B")
	require.Equal(t, int64(0), s.ReturningCustomers)
	require.Equal(t, int64(2), s.TotalCustomers)
	require.Equal(t, 0.0, s.RepeatRate)
	require.Equal(t, int64(3), s.OrdersFromNew)
	require.Equal(t, int64(0), s.OrdersFromReturning)
	require.Equal(t, int64(435000), s.RevenueFromNew)
	require.Equal(t, int64(0), s.RevenueFromReturning)
	require.Equal(t, int64(1), s.GuestOrders)
	require.Equal(t, int64(50000), s.GuestRevenue)
	require.Len(t, s.Daily, 2)
	require.Equal(t, "2026-10-01", s.Daily[0].Day)
	require.Equal(t, [4]int64{2, 0, 2, 285000}, [4]int64{s.Daily[0].NewCustomers, s.Daily[0].ReturningCustomers, s.Daily[0].Orders, s.Daily[0].Revenue})
	require.Equal(t, "2026-10-02", s.Daily[1].Day)
	require.Equal(t, [4]int64{0, 1, 1, 150000}, [4]int64{s.Daily[1].NewCustomers, s.Daily[1].ReturningCustomers, s.Daily[1].Orders, s.Daily[1].Revenue})

	e.getJSON(a, "/api/v1/admin/reports/customers?from=2026-10-02&to=2026-10-03", &cust)
	s = cust.Data
	require.Equal(t, int64(0), s.NewCustomers)
	require.Equal(t, int64(1), s.ReturningCustomers)
	require.Equal(t, int64(1), s.OrdersFromReturning)
	require.Equal(t, int64(150000), s.RevenueFromReturning)
	require.Equal(t, int64(1), s.GuestOrders)
	require.Equal(t, int64(50000), s.GuestRevenue)

	// Item-level reports: gross subtotal, partial refunds keep full item value.
	var cat categoryReport
	e.getJSON(a, "/api/v1/admin/reports/sales-by-category?"+bothDays, &cat)
	require.Equal(t, int64(500000), cat.Data.TotalRevenue)
	require.Equal(t, int64(5), cat.Data.TotalUnits)
	require.Len(t, cat.Data.Rows, 2)
	require.NotNil(t, cat.Data.Rows[0].CategoryID)
	require.Equal(t, e.c1, *cat.Data.Rows[0].CategoryID)
	require.Equal(t, categoryRow{CategoryID: cat.Data.Rows[0].CategoryID, CategoryName: "Laptop", UnitsSold: 3, Orders: 2, Revenue: 300000, Share: 0.6}, cat.Data.Rows[0])
	require.Equal(t, categoryRow{CategoryName: "Uncategorized", UnitsSold: 2, Orders: 2, Revenue: 200000, Share: 0.4}, cat.Data.Rows[1])

	var prod productReport
	e.getJSON(a, "/api/v1/admin/reports/sales-by-product?"+bothDays, &prod)
	require.Equal(t, 2, prod.Pagination.TotalItems)
	require.Len(t, prod.Data, 2)
	require.Equal(t, e.p1, *prod.Data[0].ProductID)
	require.Equal(t, int64(300000), prod.Data[0].Revenue)
	require.Equal(t, e.p2, *prod.Data[1].ProductID)
	require.Equal(t, int64(200000), prod.Data[1].Revenue)
	require.Nil(t, prod.Data[1].CategoryID)
	e.getJSON(a, "/api/v1/admin/reports/sales-by-product?"+bothDays+"&categoryId="+e.c1, &prod)
	require.Equal(t, 1, prod.Pagination.TotalItems)
	require.Len(t, prod.Data, 1)
	require.Equal(t, e.p1, *prod.Data[0].ProductID)

	// Top products bounded to D1.
	var top struct {
		Data []struct {
			ProductID string `json:"product_id"`
			QtySold   int64  `json:"qty_sold"`
			Gross     int64  `json:"gross"`
		}
	}
	e.getJSON(a, "/api/v1/analytics/top-products?from=2026-09-30T17:00:00Z&to=2026-10-01T17:00:00Z", &top)
	require.Len(t, top.Data, 1)
	require.Equal(t, e.p1, top.Data[0].ProductID)
	require.Equal(t, int64(3), top.Data[0].QtySold)
	require.Equal(t, int64(300000), top.Data[0].Gross)

	// Consistency across endpoints.
	require.Equal(t, ov.Data.TotalRevenue, salesSum)
	require.Equal(t, ov.Data.TotalRevenue, conv.Data.PaidRevenue)
	e.getJSON(a, "/api/v1/admin/reports/customers?"+bothDays, &cust)
	require.Equal(t, ov.Data.TotalRevenue, cust.Data.RevenueFromNew+cust.Data.RevenueFromReturning+cust.Data.GuestRevenue)
	require.Equal(t, ov.Data.RevenueOrders, conv.Data.PaidOrders)

	// Product CSV.
	rec := e.get(a, "/api/v1/admin/reports/sales-by-product?"+bothDays+"&format=csv")
	require.Equal(t, "text/csv; charset=utf-8", rec.Header().Get("Content-Type"))
	require.Equal(t, `attachment; filename="sales-by-product_2026-10-01_2026-10-03.csv"`, rec.Header().Get("Content-Disposition"))
	body := rec.Body.String()
	require.True(t, strings.HasPrefix(body, "\xEF\xBB\xBF"))
	records, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(body, "\xEF\xBB\xBF"))).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 3)
	require.Equal(t, "product_id", records[0][0])

	// UTC control: the same instant lands on the previous day for a UTC tenant.
	e.exec(`UPDATE tenants SET timezone = 'UTC' WHERE id = $1`, a)
	e.getJSON(a, "/api/v1/analytics/sales?from=2026-09-30T00:00:00Z&to=2026-10-01T00:00:00Z", &sales)
	require.Equal(t, []salesDay{{Day: "2026-09-30T00:00:00Z", PaidOrders: 1, AllOrders: 1, Revenue: 90000}}, sales.Data)
}

func TestReportsIntegrationTenantB(t *testing.T) {
	e := newRTest(t)
	b := e.tenB

	var sales struct{ Data []salesDay }
	e.getJSON(b, "/api/v1/analytics/sales?from=2026-10-01T00:00:00Z&to=2026-10-03T00:00:00Z", &sales)
	require.Equal(t, []salesDay{{Day: "2026-10-01T00:00:00Z", PaidOrders: 1, AllOrders: 2, Revenue: 3000000}}, sales.Data)

	var cust customersReport
	e.getJSON(b, "/api/v1/admin/reports/customers?"+bothDays, &cust)
	require.Equal(t, "UTC", cust.Data.Timezone)
	require.Equal(t, int64(0), cust.Data.NewCustomers)
	require.Equal(t, int64(1), cust.Data.ReturningCustomers)
	require.Equal(t, int64(3000000), cust.Data.RevenueFromReturning)
	require.Equal(t, int64(0), cust.Data.GuestOrders)

	var conv conversionReport
	e.getJSON(b, "/api/v1/admin/reports/conversion?"+bothDays, &conv)
	require.Equal(t, int64(2), conv.Data.CartsCreated)
	require.Equal(t, int64(2), conv.Data.CheckoutsStarted)
	require.Equal(t, int64(1), conv.Data.PaidOrders)
	require.Equal(t, int64(1), conv.Data.RefundedOrders)
	require.Equal(t, int64(3000000), conv.Data.PaidRevenue)

	var cat categoryReport
	e.getJSON(b, "/api/v1/admin/reports/sales-by-category?"+bothDays, &cat)
	require.Len(t, cat.Data.Rows, 1)
	require.Equal(t, "Decoy", cat.Data.Rows[0].CategoryName)
	require.Equal(t, int64(3000000), cat.Data.Rows[0].Revenue)

	var ov statsBody
	e.getJSON(b, "/api/v1/admin/analytics/overview?range=all", &ov)
	require.Equal(t, int64(8000000), ov.Data.TotalRevenue)
	require.Equal(t, int64(2), ov.Data.RevenueOrders)
	require.Equal(t, int64(3), ov.Data.TotalOrders)
}

// An empty range and a guest-only range must return 200 with zeros/empty
// lists (the customers summary used to risk pgx.ErrNoRows here).
func TestReportsIntegrationEmptyAndGuestOnlyRanges(t *testing.T) {
	e := newRTest(t)
	a := e.tenA

	const empty = "from=2026-11-01&to=2026-11-02"
	var cat categoryReport
	e.getJSON(a, "/api/v1/admin/reports/sales-by-category?"+empty, &cat)
	require.Empty(t, cat.Data.Rows)
	require.NotNil(t, cat.Data.Rows)
	require.Equal(t, int64(0), cat.Data.TotalRevenue)

	var prod productReport
	e.getJSON(a, "/api/v1/admin/reports/sales-by-product?"+empty, &prod)
	require.Empty(t, prod.Data)
	require.Equal(t, 0, prod.Pagination.TotalItems)

	var conv conversionReport
	e.getJSON(a, "/api/v1/admin/reports/conversion?"+empty, &conv)
	require.Zero(t, conv.Data.CartsCreated+conv.Data.CheckoutsStarted+conv.Data.PaidOrders+conv.Data.PaidRevenue)
	require.Zero(t, conv.Data.ConversionRate)

	var cust customersReport
	e.getJSON(a, "/api/v1/admin/reports/customers?"+empty, &cust)
	require.Zero(t, cust.Data.TotalCustomers+cust.Data.GuestOrders+cust.Data.GuestRevenue)
	require.Empty(t, cust.Data.Daily)

	rec := e.get(a, "/api/v1/admin/reports/customers?"+empty+"&format=csv")
	require.Equal(t, "\xEF\xBB\xBFday,new_customers,returning_customers,orders,revenue\n", rec.Body.String())

	var sales struct{ Data []salesDay }
	e.getJSON(a, "/api/v1/analytics/sales?from=2026-10-31T17:00:00Z&to=2026-11-01T17:00:00Z", &sales)
	require.Empty(t, sales.Data)

	var stats statsBody
	e.getJSON(a, "/api/v1/admin/orders/stats?startDate=2026-11-01&endDate=2026-11-02", &stats)
	require.Zero(t, stats.Data.TotalRevenue+stats.Data.RevenueOrders+stats.Data.TotalOrders+stats.Data.AverageOrderValue)

	var overview struct {
		Data struct {
			TotalRevenue int64 `json:"totalRevenue"`
			PaidOrders   int64 `json:"paidOrders"`
		} `json:"data"`
	}
	e.getJSON(a, "/api/v1/analytics/overview?from=2026-10-31T17:00:00Z&to=2026-11-01T17:00:00Z", &overview)
	require.Zero(t, overview.Data.TotalRevenue+overview.Data.PaidOrders)

	// Guest-only: just A5 (guest, SHIPPED, 50000).
	const guestOnly = "from=2026-10-02T05:30:00Z&to=2026-10-02T06:30:00Z"
	e.getJSON(a, "/api/v1/admin/reports/customers?"+guestOnly, &cust)
	require.Zero(t, cust.Data.TotalCustomers)
	require.Equal(t, int64(1), cust.Data.GuestOrders)
	require.Equal(t, int64(50000), cust.Data.GuestRevenue)
	require.Empty(t, cust.Data.Daily)

	e.getJSON(a, "/api/v1/admin/reports/conversion?"+guestOnly, &conv)
	require.Equal(t, int64(1), conv.Data.CheckoutsStarted)
	require.Equal(t, int64(1), conv.Data.PaidOrders)
	require.Equal(t, int64(50000), conv.Data.PaidRevenue)

	e.getJSON(a, "/api/v1/admin/reports/sales-by-category?"+guestOnly, &cat)
	require.Len(t, cat.Data.Rows, 1)
	require.Equal(t, int64(50000), cat.Data.Rows[0].Revenue)

	e.getJSON(a, "/api/v1/admin/reports/sales-by-product?"+guestOnly, &prod)
	require.Len(t, prod.Data, 1)
	require.Equal(t, e.p2, *prod.Data[0].ProductID)
}
