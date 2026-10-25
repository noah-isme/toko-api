package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// ErrTenantNotFound is returned when the tenant in the request context has no
// tenants row, so no timezone (and no data) can be resolved for it.
var ErrTenantNotFound = errors.New("analytics: tenant not found")

// Querier defines the database access required for analytics operations.
// Every query is tenant-scoped; see internal/db/queries/analytics.sql for the
// revenue rule.
type Querier interface {
	GetSalesDailyRange(ctx context.Context, arg dbgen.GetSalesDailyRangeParams) ([]dbgen.GetSalesDailyRangeRow, error)
	GetTopProducts(ctx context.Context, arg dbgen.GetTopProductsParams) ([]dbgen.GetTopProductsRow, error)
	GetTenantTimezone(ctx context.Context, id pgtype.UUID) (string, error)
}

// Service provides cached, tenant-scoped access to live analytics aggregates.
type Service struct {
	Q            Querier
	R            *redis.Client
	TTL          time.Duration
	DefaultRange int
	Now          func() time.Time
	Prefix       string
}

// SalesDay is one tenant-local day of the legacy sales series. Day is the
// tenant-local midnight, so it serialises with the tenant offset (for example
// 2026-10-01T00:00:00+07:00).
type SalesDay struct {
	Day        time.Time `json:"day"`
	PaidOrders int64     `json:"paid_orders"`
	AllOrders  int64     `json:"all_orders"`
	Revenue    int64     `json:"revenue"`
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

// cacheInstant renders a bound for a cache key. Bounds are full instants (not
// dates): two ranges on the same calendar date but in different offsets are
// different ranges and must not share a cache entry.
func cacheInstant(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func cacheBound(t pgtype.Timestamptz) string {
	if !t.Valid {
		return "-"
	}
	return cacheInstant(t.Time)
}

func uuidKey(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}

// Location returns the tenant's configured timezone. An empty or unknown name
// falls back to UTC with a warning; a missing tenant row is ErrTenantNotFound.
func (s *Service) Location(ctx context.Context, tenantID pgtype.UUID) (*time.Location, error) {
	if s == nil || s.Q == nil {
		return nil, fmt.Errorf("analytics service not configured")
	}
	name, err := s.Q.GetTenantTimezone(ctx, tenantID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTenantNotFound
		}
		return nil, fmt.Errorf("load tenant timezone: %w", err)
	}
	loc, ok := tenant.Location(name)
	if !ok {
		zerolog.Ctx(ctx).Warn().Str("tenant_id", uuidKey(tenantID)).Str("timezone", name).Msg("invalid tenant timezone, falling back to UTC")
	}
	return loc, nil
}

// SalesRange returns the tenant's daily sales in [from, to), bucketed by
// tenant-local day.
func (s *Service) SalesRange(ctx context.Context, tenantID pgtype.UUID, loc *time.Location, from, to time.Time) ([]SalesDay, error) {
	if s == nil || s.Q == nil {
		return nil, fmt.Errorf("analytics service not configured")
	}
	if !tenantID.Valid {
		return nil, ErrTenantNotFound
	}
	if loc == nil {
		loc = time.UTC
	}
	key := s.key("analytics", "sales", uuidKey(tenantID), loc.String(), cacheInstant(from), cacheInstant(to))
	if days, ok := s.getSalesFromCache(ctx, key, loc); ok {
		return days, nil
	}
	rows, err := s.Q.GetSalesDailyRange(ctx, dbgen.GetSalesDailyRangeParams{
		Tz:        loc.String(),
		TenantID:  tenantID,
		StartDate: pgtype.Timestamptz{Time: from, Valid: true},
		EndDate:   pgtype.Timestamptz{Time: to, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	days := make([]SalesDay, 0, len(rows))
	for _, row := range rows {
		if !row.Day.Valid {
			continue
		}
		y, m, d := row.Day.Time.Date()
		days = append(days, SalesDay{
			Day:        time.Date(y, m, d, 0, 0, 0, 0, loc),
			PaidOrders: row.PaidOrders,
			AllOrders:  row.AllOrders,
			Revenue:    row.Revenue,
		})
	}
	s.store(ctx, key, days)
	return days, nil
}

// TopProducts returns the tenant's top-selling products ordered by quantity
// sold, optionally bounded by [from, to) (an invalid bound means unbounded).
func (s *Service) TopProducts(ctx context.Context, tenantID pgtype.UUID, from, to pgtype.Timestamptz, limit, offset int32) ([]dbgen.GetTopProductsRow, error) {
	if s == nil || s.Q == nil {
		return nil, fmt.Errorf("analytics service not configured")
	}
	if !tenantID.Valid {
		return nil, ErrTenantNotFound
	}
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	key := s.key("analytics", "top", uuidKey(tenantID), cacheBound(from), cacheBound(to), limit, offset)
	if rows, ok := s.getTopFromCache(ctx, key); ok {
		return rows, nil
	}
	rows, err := s.Q.GetTopProducts(ctx, dbgen.GetTopProductsParams{
		TenantID:   tenantID,
		StartAt:    from,
		EndAt:      to,
		OffsetRows: offset,
		LimitCount: limit,
	})
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []dbgen.GetTopProductsRow{}
	}
	s.store(ctx, key, rows)
	return rows, nil
}

func (s *Service) getSalesFromCache(ctx context.Context, key string, loc *time.Location) ([]SalesDay, bool) {
	if s.R == nil || s.TTL <= 0 || strings.TrimSpace(key) == "" {
		return nil, false
	}
	data, err := s.R.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	var days []SalesDay
	if err := json.Unmarshal(data, &days); err != nil {
		return nil, false
	}
	// JSON keeps only the offset; restore the named zone for consistent output.
	for i := range days {
		days[i].Day = days[i].Day.In(loc)
	}
	return days, true
}

func (s *Service) getTopFromCache(ctx context.Context, key string) ([]dbgen.GetTopProductsRow, bool) {
	if s.R == nil || s.TTL <= 0 || strings.TrimSpace(key) == "" {
		return nil, false
	}
	data, err := s.R.Get(ctx, key).Bytes()
	if err != nil {
		return nil, false
	}
	var rows []dbgen.GetTopProductsRow
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
