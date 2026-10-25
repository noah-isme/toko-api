package catalog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// DefaultPopularityRefreshInterval is used when PopularityRefresher.Interval is not set.
const DefaultPopularityRefreshInterval = 5 * time.Minute

// PopularityRefresher periodically recomputes product_sales (units sold in paid
// orders, per product) from order_items JOIN orders, one tenant per short
// transaction. It is the only writer of product_sales. Keeping the counter out of
// the payment-settlement path means paid webhooks never contend on a shared row;
// the price is that the popularity sort can be up to one Interval stale.
type PopularityRefresher struct {
	Pool     *pgxpool.Pool
	Interval time.Duration
	Logger   *zerolog.Logger
}

// PopularityRefreshStats summarises one RefreshAll pass.
type PopularityRefreshStats struct {
	Tenants  int
	Skipped  int
	Upserted int64
	Deleted  int64
}

// Run refreshes immediately and then on every Interval until ctx is cancelled.
// Errors are logged and the loop continues; it returns ctx.Err() on shutdown.
func (r PopularityRefresher) Run(ctx context.Context) error {
	interval := r.Interval
	if interval <= 0 {
		interval = DefaultPopularityRefreshInterval
	}
	r.runOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

func (r PopularityRefresher) runOnce(ctx context.Context) {
	started := time.Now()
	stats, err := r.RefreshAll(ctx)
	if r.Logger == nil {
		return
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		r.Logger.Error().Err(err).Str("job", "product_sales_refresh").Msg("popularity refresh failed")
		return
	}
	r.Logger.Info().
		Str("job", "product_sales_refresh").
		Int("tenants", stats.Tenants).
		Int("skipped_locked", stats.Skipped).
		Int64("upserted", stats.Upserted).
		Int64("deleted", stats.Deleted).
		Dur("duration", time.Since(started)).
		Msg("popularity refresh complete")
}

// RefreshAll recomputes product_sales for every tenant. Per-tenant failures do not
// stop the pass; the first error is returned after all tenants were attempted.
func (r PopularityRefresher) RefreshAll(ctx context.Context) (PopularityRefreshStats, error) {
	var stats PopularityRefreshStats
	if r.Pool == nil {
		return stats, errors.New("popularity refresher: pool not configured")
	}
	tenantIDs, err := dbgen.New(r.Pool).ListTenantIDsForProductSales(ctx)
	if err != nil {
		return stats, fmt.Errorf("list tenants: %w", err)
	}
	var firstErr error
	for _, tenantID := range tenantIDs {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		refreshed, upserted, deleted, err := r.RefreshTenant(ctx, tenantID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !refreshed {
			stats.Skipped++
			continue
		}
		stats.Tenants++
		stats.Upserted += upserted
		stats.Deleted += deleted
	}
	return stats, firstErr
}

// RefreshTenant recomputes one tenant's counters in a single transaction. It returns
// refreshed=false without changes when another refresher holds the tenant's advisory lock.
func (r PopularityRefresher) RefreshTenant(ctx context.Context, tenantID pgtype.UUID) (refreshed bool, upserted, deleted int64, err error) {
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return false, 0, 0, fmt.Errorf("begin product_sales refresh: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := dbgen.New(tx)
	locked, err := q.TryLockProductSalesTenant(ctx, tenantID)
	if err != nil {
		return false, 0, 0, fmt.Errorf("lock product_sales tenant: %w", err)
	}
	if !locked {
		return false, 0, 0, nil
	}
	if upserted, err = q.UpsertProductSalesForTenant(ctx, tenantID); err != nil {
		return false, 0, 0, fmt.Errorf("upsert product_sales: %w", err)
	}
	if deleted, err = q.DeleteStaleProductSalesForTenant(ctx, tenantID); err != nil {
		return false, 0, 0, fmt.Errorf("delete stale product_sales: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, 0, 0, fmt.Errorf("commit product_sales refresh: %w", err)
	}
	return true, upserted, deleted, nil
}
