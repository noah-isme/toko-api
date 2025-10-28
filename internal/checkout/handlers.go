package checkout

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/noah-isme/backend-toko/internal/common"
)

type Handler struct {
	Svc *Service
}

func (h *Handler) Checkout(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "checkout service not configured", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok || userID == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}
	var payload Input
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	out, err := h.Svc.Create(r.Context(), &userID, payload)
	if err != nil {
		h.writeError(w, err)
		return
	}
	common.JSON(w, http.StatusCreated, map[string]any{"data": out})
}

func (h *Handler) writeError(w http.ResponseWriter, err error) {
	if err == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "unknown error", nil)
		return
	}
	var appErr *common.AppError
	if errors.As(err, &appErr) {
		status := appErr.HTTPStatus
		if status == 0 {
			status = http.StatusBadRequest
		}
		code := appErr.Code
		if code == "" {
			code = "BAD_REQUEST"
		}
		common.JSONError(w, status, code, appErr.Message, appErr.Details)
		return
	}
	common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
}
