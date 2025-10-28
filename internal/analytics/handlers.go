package analytics

import (
	"net/http"
	"time"

	"github.com/noah-isme/backend-toko/internal/common"
)

// Handler exposes analytics read endpoints.
type Handler struct {
	Svc *Service
}

// Sales returns aggregated sales metrics for the requested range.
func (h *Handler) Sales(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "ANALYTICS_NOT_CONFIGURED", "analytics service not configured", nil)
		return
	}
	query := r.URL.Query()
	fromStr := query.Get("from")
	toStr := query.Get("to")
	now := h.Svc.now()
	var (
		from time.Time
		to   time.Time
		err  error
	)
	if fromStr != "" && toStr != "" {
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
		days := h.Svc.DefaultRange
		if days <= 0 {
			days = 30
		}
		if raw := query.Get("days"); raw != "" {
			parsed := common.AtoiDefault(raw, days)
			if parsed > 0 {
				days = parsed
			}
		}
		to = now
		from = to.AddDate(0, 0, -days)
	}
	if !from.Before(to) {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "from must be before to", nil)
		return
	}
	rows, err := h.Svc.SalesRange(r.Context(), from, to)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "ANALYTICS_ERROR", err.Error(), nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// TopProducts returns the top selling products within the analytics view.
func (h *Handler) TopProducts(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "ANALYTICS_NOT_CONFIGURED", "analytics service not configured", nil)
		return
	}
	q := r.URL.Query()
	limit := common.AtoiDefault(q.Get("limit"), 10)
	offset := common.AtoiDefault(q.Get("offset"), 0)
	if limit <= 0 {
		limit = 10
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := h.Svc.TopProducts(r.Context(), int32(limit), int32(offset))
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "ANALYTICS_ERROR", err.Error(), nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// Overview aggregates key analytics metrics for dashboards.
func (h *Handler) Overview(w http.ResponseWriter, r *http.Request) {
	common.JSONError(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "overview will be available soon", nil)
}
