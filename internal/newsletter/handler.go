package newsletter

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type Handler struct {
	Svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{Svc: svc}
}

type subscribeRequest struct {
	Email string `json:"email"`
}

func (h *Handler) Subscribe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req subscribeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "INVALID_BODY", "invalid request body", err.Error())
		return
	}

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "missing tenant context", nil)
		return
	}

	sub, err := h.Svc.Subscribe(ctx, tenantID, req.Email)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return
	}

	common.JSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"message": "Thank you for subscribing to our newsletter!",
			"email":   sub.Email,
			"status":  sub.Status,
		},
	})
}

func (h *Handler) AdminList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tenantID, ok := tenant.FromContext(ctx)
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "missing tenant context", nil)
		return
	}

	limit := int32(20)
	offset := int32(0)
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.ParseInt(l, 10, 32); err == nil && parsed > 0 {
			limit = int32(parsed)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if parsed, err := strconv.ParseInt(o, 10, 32); err == nil && parsed >= 0 {
			offset = int32(parsed)
		}
	}

	items, total, err := h.Svc.List(ctx, tenantID, limit, offset)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to list newsletter subscribers", err.Error())
		return
	}

	common.JSON(w, http.StatusOK, map[string]any{
		"data": items,
		"meta": map[string]any{
			"total":  total,
			"limit":  limit,
			"offset": offset,
		},
	})
}
