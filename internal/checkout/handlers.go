package checkout

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5/pgconn"

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

func (h *Handler) CreateDraft(w http.ResponseWriter, r *http.Request) {
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
	out, err := h.Svc.CreateDraft(r.Context(), &userID, payload)
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
	if isLockConflict(err) {
		// A concurrent checkout or admin catalog write held the rows: nothing
		// was committed, so the client can safely retry. Never leak the raw
		// driver message (it carries the SQLSTATE and relation names).
		common.JSONError(w, http.StatusConflict, "CONFLICT", "checkout is busy, please retry", map[string]any{"retryable": true})
		return
	}
	common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
}

// isLockConflict reports deadlock (40P01), lock timeout (55P03) and
// serialization (40001) failures, which are transient and retryable.
func isLockConflict(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "40P01", "55P03", "40001":
			return true
		}
	}
	return false
}
