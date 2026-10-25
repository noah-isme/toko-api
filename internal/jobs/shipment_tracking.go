package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/cart"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/resilience"
	"github.com/noah-isme/backend-toko/internal/shipping"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// ShipmentTrackingJobName is the job's name and lock suffix.
const ShipmentTrackingJobName = "shipment_tracking"

// trackingQueries is the subset of dbgen.Queries the tracking job uses.
type trackingQueries interface {
	ListActiveTenantIDs(ctx context.Context) ([]pgtype.UUID, error)
	ListShipmentsDueForTracking(ctx context.Context, arg dbgen.ListShipmentsDueForTrackingParams) ([]dbgen.ListShipmentsDueForTrackingRow, error)
	UpdateShipmentTrackingPoll(ctx context.Context, arg dbgen.UpdateShipmentTrackingPollParams) error
	StopTrackingStaleShipments(ctx context.Context, arg dbgen.StopTrackingStaleShipmentsParams) (int64, error)
}

// shipmentAppender records a tracking event through the shipping service so
// the shipment state machine, the order status sync, customer mail and
// domain events all follow the existing path.
type shipmentAppender interface {
	AppendEventOpts(ctx context.Context, orderID pgtype.UUID, status dbgen.ShipmentStatus, description, location *string, occurredAt *time.Time, payload []byte, opts shipping.AppendOptions) (dbgen.ShipmentEvent, dbgen.Shipment, error)
}

// BreakerProvider guards a tracking provider with a circuit breaker. While
// the breaker is open it returns resilience.ErrOpenCircuit without calling the
// carrier.
type BreakerProvider struct {
	Inner   shipping.Provider
	Breaker *resilience.Breaker
}

// TrackWithSummary implements shipping.TrackerWithSummary.
func (p BreakerProvider) TrackWithSummary(ctx context.Context, req shipping.TrackReq) (shipping.TrackResult, error) {
	if p.Inner == nil {
		return shipping.TrackResult{}, errors.New("tracking provider not configured")
	}
	if p.Breaker != nil && !p.Breaker.Allow(ctx) {
		return shipping.TrackResult{}, resilience.ErrOpenCircuit
	}
	var (
		res shipping.TrackResult
		err error
	)
	if ts, ok := p.Inner.(shipping.TrackerWithSummary); ok {
		res, err = ts.TrackWithSummary(ctx, req)
	} else {
		res.Events, err = p.Inner.Track(ctx, req)
	}
	if p.Breaker != nil {
		p.Breaker.Report(ctx, err == nil)
	}
	return res, err
}

// ShipmentTrackingJob polls the carrier for shipments that are SHIPPED or
// OUT_FOR_DELIVERY and applies forward status changes. It holds no database
// row lock across the carrier call; single-runner exclusivity comes from the
// job lock and the bookkeeping update is keyed by shipment id and tenant.
type ShipmentTrackingJob struct {
	Q        trackingQueries
	Svc      shipmentAppender
	Provider shipping.TrackerWithSummary
	// BatchSize is the per-tenant page size.
	BatchSize int
	// MaxCallsPerTick caps carrier calls per run across all tenants.
	MaxCallsPerTick int
	// MaxAttempts stops polling after this many consecutive polls without
	// progress or with errors.
	MaxAttempts int
	// MaxAgeDays stops polling shipments whose last event is older.
	MaxAgeDays  int
	BackoffBase time.Duration
	BackoffMax  time.Duration
	// SilentAfter: events older than this (carrier time) are applied without
	// customer mail or domain events (historical backlog).
	SilentAfter time.Duration
	// Jitter is the error-backoff jitter fraction (e.g. 0.2).
	Jitter float64
	Logger *zerolog.Logger
	Now    func() time.Time
}

// Name implements Job.
func (j *ShipmentTrackingJob) Name() string { return ShipmentTrackingJobName }

// Run implements Job.
func (j *ShipmentTrackingJob) Run(ctx context.Context) (Result, error) {
	if j.Q == nil || j.Svc == nil || j.Provider == nil {
		return nil, errors.New("shipment_tracking: not configured")
	}
	res := Result{}
	tenants, err := j.Q.ListActiveTenantIDs(ctx)
	if err != nil {
		return res, fmt.Errorf("list tenants: %w", err)
	}
	callsLeft := j.maxCalls()
	var firstErr error
	for _, tenantID := range tenants {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		tctx := tenant.WithTenant(ctx, cart.UUIDString(tenantID))
		stopped, err := j.Q.StopTrackingStaleShipments(tctx, dbgen.StopTrackingStaleShipmentsParams{TenantID: tenantID, MaxAgeDays: int32(j.maxAgeDays())})
		if err != nil {
			j.logger().Error().Err(err).Str("tenant_id", cart.UUIDString(tenantID)).Msg("shipment_tracking: stop stale failed")
			if firstErr == nil {
				firstErr = err
			}
		}
		res.Add("stopped_max_age", int(stopped))
		if callsLeft <= 0 {
			// Keep stopping stale rows for every tenant, but make no calls;
			// due shipments keep their schedule for the next tick.
			res.Add("deferred_cap", 1)
			continue
		}
		limit := j.batchSize()
		if limit > callsLeft {
			limit = callsLeft
		}
		rows, err := j.Q.ListShipmentsDueForTracking(tctx, dbgen.ListShipmentsDueForTrackingParams{
			TenantID:   tenantID,
			MaxAgeDays: int32(j.maxAgeDays()),
			BatchSize:  int32(limit),
		})
		if err != nil {
			j.logger().Error().Err(err).Str("tenant_id", cart.UUIDString(tenantID)).Msg("shipment_tracking: list due failed")
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if limit < j.batchSize() && len(rows) == limit {
			// The call cap cut this page short; the rest keep their
			// schedule and are picked up next tick, oldest first.
			res.Add("deferred_cap", 1)
		}
		breakerOpen := false
		for _, row := range rows {
			callsLeft--
			if err := j.pollOne(tctx, row, res); err != nil {
				if errors.Is(err, resilience.ErrOpenCircuit) {
					breakerOpen = true
					break
				}
				if firstErr == nil {
					firstErr = err
				}
			}
		}
		if breakerOpen {
			res.Add("skipped_breaker", 1)
			// The carrier is unhealthy: stop calling it for this run.
			break
		}
	}
	return res, firstErr
}

// pollOne polls one shipment and records the outcome. It returns
// resilience.ErrOpenCircuit (without touching the row) when the breaker is
// open, and a database error when the bookkeeping update fails. Carrier and
// apply errors are recorded on the row and not returned.
func (j *ShipmentTrackingJob) pollOne(ctx context.Context, row dbgen.ListShipmentsDueForTrackingRow, res Result) error {
	logger := j.logger().With().Str("shipment_id", cart.UUIDString(row.ID)).Str("tenant_id", cart.UUIDString(row.TenantID)).Logger()
	result, err := j.Provider.TrackWithSummary(ctx, shipping.TrackReq{Courier: row.Courier.String, TrackingNumber: row.TrackingNumber.String})
	if errors.Is(err, resilience.ErrOpenCircuit) {
		return err
	}
	res.Add("polled", 1)
	attempts := int(row.TrackAttempts) + 1
	if err != nil {
		res.Add("errors", 1)
		logger.Warn().Err(err).Int("attempts", attempts).Msg("shipment_tracking: carrier call failed")
		return j.recordError(ctx, row, attempts, truncate("provider: "+err.Error()), res)
	}
	if result.Terminal != "" {
		res.Add("stopped_terminal", 1)
		return j.stop(ctx, row, attempts, "stopped: "+strings.ToLower(result.Terminal))
	}

	current := shipmentRank(currentShipmentStatus(row))
	evs := append([]shipping.TrackEvent(nil), result.Events...)
	sort.SliceStable(evs, func(a, b int) bool { return evs[a].OccurredAt < evs[b].OccurredAt })
	applied := 0
	for _, ev := range evs {
		st := shipping.MapExternalToStatus(ev.Status)
		if shipmentRank(st) <= current {
			continue
		}
		var occurredAt *time.Time
		silent := false
		if ev.OccurredAt > 0 {
			t := time.Unix(ev.OccurredAt, 0).UTC()
			occurredAt = &t
			silent = j.SilentAfter > 0 && j.now().Sub(t) > j.SilentAfter
		}
		desc, loc := ev.Description, ev.Location
		raw, _ := json.Marshal(map[string]any{
			"source":      "tracking_poll",
			"status":      ev.Status,
			"description": ev.Description,
			"location":    ev.Location,
			"occurredAt":  ev.OccurredAt,
		})
		_, _, err := j.Svc.AppendEventOpts(ctx, row.OrderID, st, nonEmpty(desc), nonEmpty(loc), occurredAt, raw, shipping.AppendOptions{Silent: silent})
		if err != nil {
			if errors.Is(err, shipping.ErrInvalidShipmentTransition) {
				continue
			}
			res.Add("errors", 1)
			logger.Warn().Err(err).Str("status", string(st)).Msg("shipment_tracking: apply event failed")
			return j.recordError(ctx, row, attempts, truncate("apply: "+err.Error()), res)
		}
		current = shipmentRank(st)
		applied++
		res.Add("events_applied", 1)
		if silent {
			res.Add("events_applied_silent", 1)
		}
	}
	if current >= shipmentRank(dbgen.ShipmentStatusDELIVERED) {
		res.Add("stopped_delivered", 1)
		return j.stop(ctx, row, 0, "stopped: delivered")
	}
	if applied > 0 {
		return j.schedule(ctx, row, 0, j.now().Add(j.base()), pgtype.Text{})
	}
	res.Add("no_change", 1)
	if attempts >= j.maxAttempts() {
		res.Add("stopped_max_attempts", 1)
		return j.stop(ctx, row, attempts, "stopped: max_attempts")
	}
	return j.schedule(ctx, row, attempts, j.now().Add(j.noChangeDelay(attempts)), pgtype.Text{})
}

func (j *ShipmentTrackingJob) recordError(ctx context.Context, row dbgen.ListShipmentsDueForTrackingRow, attempts int, msg string, res Result) error {
	if attempts >= j.maxAttempts() {
		res.Add("stopped_max_attempts", 1)
		return j.stop(ctx, row, attempts, "stopped: max_attempts")
	}
	return j.schedule(ctx, row, attempts, j.now().Add(j.errorDelay(attempts)), pgtype.Text{String: msg, Valid: true})
}

func (j *ShipmentTrackingJob) stop(ctx context.Context, row dbgen.ListShipmentsDueForTrackingRow, attempts int, reason string) error {
	return j.update(ctx, row, attempts, pgtype.Timestamptz{}, pgtype.Text{String: reason, Valid: true})
}

func (j *ShipmentTrackingJob) schedule(ctx context.Context, row dbgen.ListShipmentsDueForTrackingRow, attempts int, next time.Time, lastErr pgtype.Text) error {
	return j.update(ctx, row, attempts, pgtype.Timestamptz{Time: next, Valid: true}, lastErr)
}

func (j *ShipmentTrackingJob) update(ctx context.Context, row dbgen.ListShipmentsDueForTrackingRow, attempts int, next pgtype.Timestamptz, lastErr pgtype.Text) error {
	if err := j.Q.UpdateShipmentTrackingPoll(ctx, dbgen.UpdateShipmentTrackingPollParams{
		TrackAttempts:   int32(attempts),
		TrackNextPollAt: next,
		TrackLastError:  lastErr,
		ID:              row.ID,
		TenantID:        row.TenantID,
	}); err != nil {
		return fmt.Errorf("update tracking poll %s: %w", cart.UUIDString(row.ID), err)
	}
	return nil
}

// noChangeDelay is min(base * 2^attempts, max).
func (j *ShipmentTrackingJob) noChangeDelay(attempts int) time.Duration {
	return capDelay(j.base(), attempts, j.max())
}

// errorDelay is resilience.Backoff with jitter, kept within [base, max].
func (j *ShipmentTrackingJob) errorDelay(attempts int) time.Duration {
	if attempts > 30 {
		return j.max()
	}
	d := resilience.Backoff(j.base(), attempts, j.Jitter)
	if d < j.base() {
		d = j.base()
	}
	if d > j.max() {
		d = j.max()
	}
	return d
}

func capDelay(base time.Duration, exp int, max time.Duration) time.Duration {
	d := base
	for i := 0; i < exp; i++ {
		d *= 2
		if d >= max || d <= 0 {
			return max
		}
	}
	if d > max {
		return max
	}
	return d
}

func currentShipmentStatus(row dbgen.ListShipmentsDueForTrackingRow) dbgen.ShipmentStatus {
	if row.LastStatus.Valid {
		return row.LastStatus.ShipmentStatus
	}
	return row.Status
}

// shipmentRank orders shipment states for forward-only application. States
// before OUT_FOR_DELIVERY rank with SHIPPED, so they are never re-applied.
func shipmentRank(st dbgen.ShipmentStatus) int {
	switch st {
	case dbgen.ShipmentStatusOUTFORDELIVERY:
		return 2
	case dbgen.ShipmentStatusDELIVERED:
		return 3
	default:
		return 1
	}
}

func nonEmpty(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

func truncate(s string) string {
	const max = 500
	if len(s) > max {
		return s[:max]
	}
	return s
}

func (j *ShipmentTrackingJob) batchSize() int {
	if j.BatchSize <= 0 {
		return 100
	}
	return j.BatchSize
}

func (j *ShipmentTrackingJob) maxCalls() int {
	if j.MaxCallsPerTick <= 0 {
		return 50
	}
	return j.MaxCallsPerTick
}

func (j *ShipmentTrackingJob) maxAttempts() int {
	if j.MaxAttempts <= 0 {
		return 200
	}
	return j.MaxAttempts
}

func (j *ShipmentTrackingJob) maxAgeDays() int {
	if j.MaxAgeDays <= 0 {
		return 30
	}
	return j.MaxAgeDays
}

func (j *ShipmentTrackingJob) base() time.Duration {
	if j.BackoffBase <= 0 {
		return 15 * time.Minute
	}
	return j.BackoffBase
}

func (j *ShipmentTrackingJob) max() time.Duration {
	if j.BackoffMax < j.base() {
		return j.base()
	}
	return j.BackoffMax
}

func (j *ShipmentTrackingJob) now() time.Time {
	if j.Now != nil {
		return j.Now()
	}
	return time.Now()
}

func (j *ShipmentTrackingJob) logger() *zerolog.Logger {
	if j.Logger != nil {
		return j.Logger
	}
	nop := zerolog.Nop()
	return &nop
}
