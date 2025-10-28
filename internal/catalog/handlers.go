package catalog

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/noah-isme/backend-toko/internal/common"
)

// Handler exposes public catalog endpoints.
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

// Brands handles GET /api/v1/brands.
func (h *Handler) Brands(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "catalog service not configured", nil)
		return
	}
	rows, err := h.service.ListBrands(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// Categories handles GET /api/v1/categories.
func (h *Handler) Categories(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "catalog service not configured", nil)
		return
	}
	rows, err := h.service.ListCategories(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": rows})
}

// Products handles GET /api/v1/products with filters, sorting, and pagination.
func (h *Handler) Products(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "catalog service not configured", nil)
		return
	}
	params, err := h.service.ParseListParams(r.URL.Query())
	if err != nil {
		h.writeError(w, err)
		return
	}
	result, err := h.service.ListProducts(r.Context(), params)
	if err != nil {
		h.writeError(w, err)
		return
	}
	w.Header().Set("X-Total-Count", strconv.FormatInt(result.Total, 10))
	common.JSON(w, http.StatusOK, map[string]any{
		"data":       result.Items,
		"pagination": common.Pagination{Page: result.Page, PerPage: result.Limit, TotalItems: int(result.Total)},
	})
}

// ProductDetail handles GET /api/v1/products/{slug}.
func (h *Handler) ProductDetail(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "catalog service not configured", nil)
		return
	}
	slug := chi.URLParam(r, "slug")
	detail, err := h.service.GetProductDetail(r.Context(), slug)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": detail})
}

// Related handles GET /api/v1/products/{slug}/related.
func (h *Handler) Related(w http.ResponseWriter, r *http.Request) {
	if h.service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "catalog service not configured", nil)
		return
	}
	slug := chi.URLParam(r, "slug")
	items, err := h.service.ListRelatedProducts(r.Context(), slug)
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
		if appErr.Err != nil {
			var syntaxErr *json.SyntaxError
			if errors.As(appErr.Err, &syntaxErr) {
				details = map[string]any{"offset": syntaxErr.Offset}
			}
		}
		common.JSONError(w, status, code, message, details)
		return
	}
	common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
}
