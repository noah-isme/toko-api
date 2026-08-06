package recommendations

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/noah-isme/backend-toko/internal/common"
)

// Handler exposes recommendation endpoints.
type Handler struct {
	service *Service
}

// HandlerConfig configures the Handler dependencies.
type HandlerConfig struct {
	Service *Service
}

// NewHandler constructs a Handler.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{service: cfg.Service}
}

// Personalized handles GET /api/v1/recommendations/personalized.
func (h *Handler) Personalized(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "recommendations service not configured", nil)
		return
	}
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 50 {
			limit = l
		}
	}
	ctx := r.Context()
	items, err := h.service.Personalized(ctx, limit)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": items})
}

// Trending handles GET /api/v1/recommendations/trending.
func (h *Handler) Trending(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "recommendations service not configured", nil)
		return
	}
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if l, err := strconv.Atoi(v); err == nil && l > 0 && l <= 50 {
			limit = l
		}
	}
	ctx := r.Context()
	items, err := h.service.Trending(ctx, limit)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": items})
}

// FrequentlyBoughtTogether handles GET /api/v1/products/{id}/frequently-bought-together.
func (h *Handler) FrequentlyBoughtTogether(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "recommendations service not configured", nil)
		return
	}
	productID := chi.URLParam(r, "id")
	if productID == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "product id is required", nil)
		return
	}
	ctx := r.Context()
	items, err := h.service.FrequentlyBoughtTogether(ctx, productID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": items})
}

// CustomersAlsoViewed handles GET /api/v1/products/{id}/also-viewed.
func (h *Handler) CustomersAlsoViewed(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "recommendations service not configured", nil)
		return
	}
	productID := chi.URLParam(r, "id")
	if productID == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "product id is required", nil)
		return
	}
	ctx := r.Context()
	items, err := h.service.CustomersAlsoViewed(ctx, productID)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": items})
}

func (h *Handler) writeError(w http.ResponseWriter, err error) {
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		status := appErr.HTTPStatus
		if status == 0 {
			status = http.StatusInternalServerError
		}
		code := appErr.Code
		if code == "" {
			code = "INTERNAL"
		}
		message := appErr.Message
		if message == "" {
			message = "internal error"
		}
		var details any
		if appErr.Details != nil {
			details = appErr.Details
		}
		common.JSONError(w, status, code, message, details)
		return
	}
	common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
}
