package user

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/noah-isme/backend-toko/internal/common"
)

// Handler exposes REST endpoints for managing address book entries.
type Handler struct {
	Service *Service
}

type addressRequest struct {
	Label        string `json:"label"`
	ReceiverName string `json:"receiver_name"`
	Phone        string `json:"phone"`
	Country      string `json:"country"`
	Province     string `json:"province"`
	City         string `json:"city"`
	PostalCode   string `json:"postal_code"`
	AddressLine1 string `json:"address_line1"`
	AddressLine2 string `json:"address_line2"`
	IsDefault    bool   `json:"is_default"`
}

// List handles GET /api/v1/users/me/addresses.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "address service not configured", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid token", nil)
		return
	}
	page, limit := common.ParsePagination(r, 20)
	addresses, total, err := h.Service.List(r.Context(), userID, page, limit)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{
		"data":       addresses,
		"pagination": common.Pagination{Page: page, PerPage: limit, TotalItems: int(total)},
	})
}

// Create handles POST /api/v1/users/me/addresses.
func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "address service not configured", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid token", nil)
		return
	}
	var req addressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request payload", nil)
		return
	}
	address, err := h.Service.Create(r.Context(), userID, toInput(req))
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusCreated, map[string]any{"data": address})
}

// Update handles PATCH /api/v1/users/me/addresses/{addressID}.
func (h *Handler) Update(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "address service not configured", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid token", nil)
		return
	}
	addressID := chi.URLParam(r, "addressID")
	if strings.TrimSpace(addressID) == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "address id is required", nil)
		return
	}
	var req addressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid request payload", nil)
		return
	}
	address, err := h.Service.Update(r.Context(), userID, addressID, toInput(req))
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": address})
}

// Delete handles DELETE /api/v1/users/me/addresses/{addressID}.
func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	if h.Service == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "address service not configured", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "missing or invalid token", nil)
		return
	}
	addressID := chi.URLParam(r, "addressID")
	if strings.TrimSpace(addressID) == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "address id is required", nil)
		return
	}
	if err := h.Service.Delete(r.Context(), userID, addressID); err != nil {
		h.writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
		common.JSONError(w, status, code, message, appErr.Details)
		return
	}
	common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "internal error", nil)
}

func toInput(req addressRequest) AddressInput {
	return AddressInput(req)
}
