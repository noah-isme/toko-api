package analytics_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/noah-isme/backend-toko/internal/analytics"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type stubQueries struct {
	salesCalls int
	topCalls   int
	timezone   string
	tzErr      error
	salesArgs  []dbgen.GetSalesDailyRangeParams
	topArgs    []dbgen.GetTopProductsParams
	salesRows  []dbgen.GetSalesDailyRangeRow
}

func (s *stubQueries) GetSalesDailyRange(ctx context.Context, arg dbgen.GetSalesDailyRangeParams) ([]dbgen.GetSalesDailyRangeRow, error) {
	s.salesCalls++
	s.salesArgs = append(s.salesArgs, arg)
	if s.salesRows != nil {
		return s.salesRows, nil
	}
	day := arg.StartDate.Time.UTC()
	return []dbgen.GetSalesDailyRangeRow{{Day: pgtype.Date{Time: time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC), Valid: true}, PaidOrders: 2, AllOrders: 3, Revenue: 1000}}, nil
}

func (s *stubQueries) GetTopProducts(ctx context.Context, arg dbgen.GetTopProductsParams) ([]dbgen.GetTopProductsRow, error) {
	s.topCalls++
	s.topArgs = append(s.topArgs, arg)
	return []dbgen.GetTopProductsRow{}, nil
}

func (s *stubQueries) GetTenantTimezone(ctx context.Context, id pgtype.UUID) (string, error) {
	if s.tzErr != nil {
		return "", s.tzErr
	}
	if s.timezone == "" {
		return "UTC", nil
	}
	return s.timezone, nil
}

func newTenantID() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}

func newCachedService(t *testing.T, q analytics.Querier) *analytics.Service {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	t.Cleanup(mr.Close)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	return &analytics.Service{Q: q, R: rdb, TTL: time.Minute, DefaultRange: 30, Prefix: "test"}
}

func TestSalesRangeCached(t *testing.T) {
	queries := &stubQueries{}
	svc := newCachedService(t, queries)
	tenantID := newTenantID()
	from := time.Now().Add(-24 * time.Hour).Truncate(24 * time.Hour)
	to := time.Now().Truncate(24 * time.Hour)
	first, err := svc.SalesRange(context.Background(), tenantID, time.UTC, from, to)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	second, err := svc.SalesRange(context.Background(), tenantID, time.UTC, from, to)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if queries.salesCalls != 1 {
		t.Fatalf("expected 1 DB call, got %d", queries.salesCalls)
	}
	if len(first) != 1 || len(second) != 1 || !first[0].Day.Equal(second[0].Day) || first[0].Revenue != second[0].Revenue {
		t.Fatalf("cached result differs: %+v vs %+v", first, second)
	}
}

func TestSalesRangeCacheIsPerTenant(t *testing.T) {
	queries := &stubQueries{}
	svc := newCachedService(t, queries)
	a, b := newTenantID(), newTenantID()
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, jakarta)
	to := from.AddDate(0, 0, 2)
	for _, id := range []pgtype.UUID{a, b, a} {
		if _, err := svc.SalesRange(context.Background(), id, jakarta, from, to); err != nil {
			t.Fatal(err)
		}
	}
	if queries.salesCalls != 2 {
		t.Fatalf("expected 2 DB calls (one per tenant), got %d", queries.salesCalls)
	}
	if queries.salesArgs[0].TenantID != a || queries.salesArgs[1].TenantID != b {
		t.Fatalf("tenant ids not passed through: %+v", queries.salesArgs)
	}
	for _, arg := range queries.salesArgs {
		if arg.Tz != "Asia/Jakarta" {
			t.Fatalf("expected Tz Asia/Jakarta, got %q", arg.Tz)
		}
	}
}

// Same calendar date, different instants: must not share a cache entry.
func TestSalesRangeCacheKeyUsesInstants(t *testing.T) {
	queries := &stubQueries{}
	svc := newCachedService(t, queries)
	id := newTenantID()
	fromWIB, _ := time.Parse(time.RFC3339, "2026-10-01T00:00:00+07:00")
	fromUTC, _ := time.Parse(time.RFC3339, "2026-10-01T00:00:00Z")
	to, _ := time.Parse(time.RFC3339, "2026-10-03T00:00:00Z")
	if _, err := svc.SalesRange(context.Background(), id, time.UTC, fromWIB, to); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SalesRange(context.Background(), id, time.UTC, fromUTC, to); err != nil {
		t.Fatal(err)
	}
	if queries.salesCalls != 2 {
		t.Fatalf("expected 2 DB calls for same-date different-instant ranges, got %d", queries.salesCalls)
	}

	topFromWIB := pgtype.Timestamptz{Time: fromWIB, Valid: true}
	topFromUTC := pgtype.Timestamptz{Time: fromUTC, Valid: true}
	if _, err := svc.TopProducts(context.Background(), id, topFromWIB, pgtype.Timestamptz{}, 10, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TopProducts(context.Background(), id, topFromUTC, pgtype.Timestamptz{}, 10, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TopProducts(context.Background(), id, topFromUTC, pgtype.Timestamptz{}, 10, 0); err != nil {
		t.Fatal(err)
	}
	if queries.topCalls != 2 {
		t.Fatalf("expected 2 top-products DB calls, got %d", queries.topCalls)
	}
}

func TestTopProductsCacheIsPerTenant(t *testing.T) {
	queries := &stubQueries{}
	svc := newCachedService(t, queries)
	a, b := newTenantID(), newTenantID()
	for _, id := range []pgtype.UUID{a, b, b} {
		if _, err := svc.TopProducts(context.Background(), id, pgtype.Timestamptz{}, pgtype.Timestamptz{}, 10, 0); err != nil {
			t.Fatal(err)
		}
	}
	if queries.topCalls != 2 {
		t.Fatalf("expected 2 DB calls, got %d", queries.topCalls)
	}
	if queries.topArgs[0].TenantID != a || queries.topArgs[1].TenantID != b {
		t.Fatalf("tenant ids not passed through: %+v", queries.topArgs)
	}
}

func TestSalesRangeDayIsLocalMidnight(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	queries := &stubQueries{salesRows: []dbgen.GetSalesDailyRangeRow{{
		Day: pgtype.Date{Time: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Valid: true}, PaidOrders: 1, AllOrders: 1, Revenue: 5,
	}}}
	svc := newCachedService(t, queries)
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, jakarta)
	for i := 0; i < 2; i++ { // second pass is served from cache
		days, err := svc.SalesRange(context.Background(), newTenantIDFixed(), jakarta, from, from.AddDate(0, 0, 1))
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(days)
		want := `[{"day":"2026-10-01T00:00:00+07:00","paid_orders":1,"all_orders":1,"revenue":5}]`
		if string(raw) != want {
			t.Fatalf("pass %d: got %s, want %s", i, raw, want)
		}
	}
	if queries.salesCalls != 1 {
		t.Fatalf("expected 1 DB call, got %d", queries.salesCalls)
	}
}

var fixedTenant = newTenantID()

func newTenantIDFixed() pgtype.UUID { return fixedTenant }

func TestLocationTenantNotFound(t *testing.T) {
	svc := &analytics.Service{Q: &stubQueries{tzErr: pgx.ErrNoRows}}
	if _, err := svc.Location(context.Background(), newTenantID()); err != analytics.ErrTenantNotFound {
		t.Fatalf("expected ErrTenantNotFound, got %v", err)
	}
}

func TestLocationInvalidFallsBackToUTC(t *testing.T) {
	svc := &analytics.Service{Q: &stubQueries{timezone: "Mars/Phobos"}}
	loc, err := svc.Location(context.Background(), newTenantID())
	if err != nil || loc != time.UTC {
		t.Fatalf("expected UTC fallback, got %v, %v", loc, err)
	}
}
