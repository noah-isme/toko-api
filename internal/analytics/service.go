package analytics

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/redis/go-redis/v9"
)

// Querier defines the database access required for analytics operations.
type Querier interface {
	GetSalesDailyRange(ctx context.Context, arg dbgen.GetSalesDailyRangeParams) ([]dbgen.GetSalesDailyRangeRow, error)
	GetTopProducts(ctx context.Context, arg dbgen.GetTopProductsParams) ([]dbgen.MvTopProduct, error)
}

// Service provides cached access to analytics materialized views.
type Service struct {
	Q            Querier
	R            *redis.Client
	TTL          time.Duration
	DefaultRange int
	Now          func() time.Time
	Prefix       string
}

func (s *Service) now() time.Time {
	if s != nil && s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) key(parts ...any) string {
	formatted := make([]string, 0, len(parts))
	prefix := strings.Trim(s.Prefix, ": ")
	if prefix != "" {
		formatted = append(formatted, prefix)
	}
	for _, part := range parts {
		formatted = append(formatted, fmt.Sprint(part))
	}
	return strings.Join(formatted, ":")
}

// SalesRange returns sales summary between the provided bounds inclusive of from and exclusive of to.
func (s *Service) SalesRange(ctx context.Context, from, to time.Time) ([]dbgen.GetSalesDailyRangeRow, error) {
	if s == nil || s.Q == nil {
		return nil, fmt.Errorf("analytics service not configured")
	}
	key := s.key("analytics", "sales", from.Format(time.DateOnly), to.Format(time.DateOnly))
	if rows, ok := s.getSalesFromCache(ctx, key); ok {
		return rows, nil
	}
	params := dbgen.GetSalesDailyRangeParams{
		StartDate: pgtype.Timestamptz{Time: from, Valid: true},
		EndDate:   pgtype.Timestamptz{Time: to, Valid: true},
	}
	rows, err := s.Q.GetSalesDailyRange(ctx, params)
	if err != nil {
		return nil, err
	}
	s.store(ctx, key, rows)
	return rows, nil
}

// TopProducts returns paginated top-selling products ordered by quantity sold.
func (s *Service) TopProducts(ctx context.Context, limit, offset int32) ([]dbgen.MvTopProduct, error) {
	if s == nil || s.Q == nil {
		return nil, fmt.Errorf("analytics service not configured")
	}
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	key := s.key("analytics", "top", limit, offset)
	if rows, ok := s.getTopFromCache(ctx, key); ok {
		return rows, nil
	}
	rows, err := s.Q.GetTopProducts(ctx, dbgen.GetTopProductsParams{OffsetRows: offset, LimitCount: limit})
	if err != nil {
		return nil, err
	}
	s.store(ctx, key, rows)
	return rows, nil
}

func (s *Service) getSalesFromCache(ctx context.Context, key string) ([]dbgen.GetSalesDailyRangeRow, bool) {
	if s.R == nil || s.TTL <= 0 || strings.TrimSpace(key) == "" {
		return nil, false
	}
	data, err := s.R.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	var rows []dbgen.GetSalesDailyRangeRow
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, false
	}
	return rows, true
}

func (s *Service) getTopFromCache(ctx context.Context, key string) ([]dbgen.MvTopProduct, bool) {
	if s.R == nil || s.TTL <= 0 || strings.TrimSpace(key) == "" {
		return nil, false
	}
	data, err := s.R.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	var rows []dbgen.MvTopProduct
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, false
	}
	return rows, true
}

func (s *Service) store(ctx context.Context, key string, value any) {
	if s.R == nil || s.TTL <= 0 || strings.TrimSpace(key) == "" {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	_ = s.R.Set(ctx, key, data, s.TTL).Err()
}

// Clear evicts cached analytics payloads.
func (s *Service) Clear(ctx context.Context) {
	if s == nil || s.R == nil {
		return
	}
	pattern := s.key("analytics", "*")
	if pattern == "" {
		pattern = "analytics:*"
	}
	iter := s.R.Scan(ctx, 0, pattern, 0).Iterator()
	for iter.Next(ctx) {
		_ = s.R.Del(ctx, iter.Val()).Err()
	}
	_ = iter.Err()
}
