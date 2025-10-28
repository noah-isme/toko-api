package analytics_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"

	"github.com/noah-isme/backend-toko/internal/analytics"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type stubQueries struct {
	salesCalls int
}

func (s *stubQueries) GetSalesDailyRange(ctx context.Context, arg dbgen.GetSalesDailyRangeParams) ([]dbgen.GetSalesDailyRangeRow, error) {
	s.salesCalls++
	return []dbgen.GetSalesDailyRangeRow{{Day: pgtype.Timestamptz{Time: arg.StartDate.Time, Valid: true}, PaidOrders: 2, AllOrders: 3, Revenue: 1000}}, nil
}

func (s *stubQueries) GetTopProducts(ctx context.Context, arg dbgen.GetTopProductsParams) ([]dbgen.MvTopProduct, error) {
	return nil, nil
}

func TestSalesRangeCached(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatalf("start miniredis: %v", err)
	}
	defer mr.Close()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	queries := &stubQueries{}
	svc := &analytics.Service{Q: queries, R: rdb, TTL: time.Minute, DefaultRange: 30}
	from := time.Now().Add(-24 * time.Hour).Truncate(24 * time.Hour)
	to := time.Now().Truncate(24 * time.Hour)
	if _, err := svc.SalesRange(context.Background(), from, to); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if _, err := svc.SalesRange(context.Background(), from, to); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if queries.salesCalls != 1 {
		t.Fatalf("expected 1 DB call, got %d", queries.salesCalls)
	}
}
