package analytics

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// Handler exposes analytics read endpoints. Every endpoint is scoped to the
// tenant in the request context and refuses to run without one.
type Handler struct {
	Svc *Service
}

// tenantID returns the tenant from the request context as a pgtype.UUID.
func tenantID(r *http.Request) (pgtype.UUID, bool) {
	raw, ok := tenant.FromContext(r.Context())
	if !ok || raw == "" {
		return pgtype.UUID{}, false
	}
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, true
}

// scope resolves the tenant and its timezone, writing the error response and
// returning ok=false when the request cannot be served.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request) (pgtype.UUID, *time.Location, bool) {
	if h == nil || h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "ANALYTICS_NOT_CONFIGURED", "analytics service not configured", nil)
		return pgtype.UUID{}, nil, false
	}
	id, ok := tenantID(r)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "TENANT_REQUIRED", "tenant is required", nil)
		return pgtype.UUID{}, nil, false
	}
	loc, err := h.Svc.Location(r.Context(), id)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return pgtype.UUID{}, nil, false
	}
	return id, loc, true
}

func writeServiceError(ctx context.Context, w http.ResponseWriter, err error) {
	if errors.Is(err, ErrTenantNotFound) {
		common.JSONError(w, http.StatusBadRequest, "TENANT_REQUIRED", "tenant is required", nil)
		return
	}
	zerolog.Ctx(ctx).Error().Err(err).Msg("analytics query failed")
	common.JSONError(w, http.StatusInternalServerError, "ANALYTICS_ERROR", "failed to load analytics", nil)
}

// defaultWindow returns [start of (tomorrow - days), start of tomorrow) in loc,
// so a default range always covers whole tenant-local days.
func defaultWindow(now time.Time, loc *time.Location, days int) (time.Time, time.Time) {
	local := now.In(loc)
	to := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
	return to.AddDate(0, 0, -days), to
}

func (h *Handler) days(raw string) int {
	days := h.Svc.DefaultRange
	if days <= 0 {
		days = 30
	}
	if raw != "" {
		if parsed := common.AtoiDefault(raw, days); parsed > 0 {
			days = parsed
		}
	}
	return days
}

// Sales returns aggregated sales metrics for the requested range.
func (h *Handler) Sales(w http.ResponseWriter, r *http.Request) {
	id, loc, ok := h.scope(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	fromStr := query.Get("from")
	toStr := query.Get("to")
	var from, to time.Time
	if fromStr != "" && toStr != "" {
		var err error
		from, err = time.Parse(time.RFC3339, fromStr)
		if err != nil {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid from date", nil)
			return
		}
		to, err = time.Parse(time.RFC3339, toStr)
		if err != nil {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid to date", nil)
			return
		}
	} else {
		from, to = defaultWindow(h.Svc.now(), loc, h.days(query.Get("days")))
	}
	if !from.Before(to) {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "from must be before to", nil)
		return
	}
	rows, err := h.Svc.SalesRange(r.Context(), id, loc, from, to)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

func optionalRFC3339(raw string) (pgtype.Timestamptz, error) {
	if raw == "" {
		return pgtype.Timestamptz{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return pgtype.Timestamptz{}, err
	}
	return pgtype.Timestamptz{Time: parsed, Valid: true}, nil
}

// TopProducts returns the tenant's top selling products (all time unless
// from/to are given).
func (h *Handler) TopProducts(w http.ResponseWriter, r *http.Request) {
	id, _, ok := h.scope(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	limit := common.AtoiDefault(q.Get("limit"), 10)
	offset := common.AtoiDefault(q.Get("offset"), 0)
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	from, err := optionalRFC3339(q.Get("from"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid from date", nil)
		return
	}
	to, err := optionalRFC3339(q.Get("to"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid to date", nil)
		return
	}
	if from.Valid && to.Valid && !from.Time.Before(to.Time) {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "from must be before to", nil)
		return
	}
	rows, err := h.Svc.TopProducts(r.Context(), id, from, to, int32(limit), int32(offset))
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// Overview aggregates key analytics metrics for dashboards.
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	id, loc, ok := h.scope(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	from, to := defaultWindow(h.Svc.now(), loc, h.days(query.Get("days")))
	if raw := query.Get("from"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid from date", nil)
			return
		}
		from = parsed
	}
	if raw := query.Get("to"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid to date", nil)
			return
		}
		to = parsed
	}
	if !from.Before(to) {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "from must be before to", nil)
		return
	}
	rows, err := h.Svc.SalesRange(r.Context(), id, loc, from, to)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	var revenue, paidOrders, allOrders int64
	for _, row := range rows {
		revenue += row.Revenue
		paidOrders += row.PaidOrders
		allOrders += row.AllOrders
	}
	average := int64(0)
	if paidOrders > 0 {
		average = revenue / paidOrders
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": map[string]any{"from": from, "to": to, "totalRevenue": revenue, "totalOrders": allOrders, "paidOrders": paidOrders, "averageOrderValue": average}})
}
