package notifications

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

const timeLayout = time.RFC3339

// Handler exposes the in-app notification HTTP endpoints.
type Handler struct {
	Svc *Service
}

// dto is the JSON shape returned to clients.
type dto struct {
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	Title     string          `json:"title"`
	Body      string          `json:"body"`
	Data      json.RawMessage `json:"data"`
	Read      bool            `json:"read"`
	ReadAt    *string         `json:"readAt"`
	CreatedAt string          `json:"createdAt"`
}

// List returns the authenticated user's notifications, most recent first.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, tenantID, ok := identity(w, r)
	if !ok {
		return
	}
	page, perPage := common.ParsePagination(r, 20)
	items, total, err := h.Svc.List(ctx, userID, tenantID, int32(page), int32(perPage))
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "list_failed", "failed to list notifications", err.Error())
		return
	}
	data := make([]dto, 0, len(items))
	for i := range items {
		data = append(data, toDTO(items[i]))
	}
	common.JSON(w, http.StatusOK, map[string]any{
		"data": data,
		"pagination": common.Pagination{
			Page:       page,
			PerPage:    perPage,
			TotalItems: int(total),
		},
	})
}

// UnreadCount returns the number of unread notifications for the user.
func (h *Handler) UnreadCount(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, tenantID, ok := identity(w, r)
	if !ok {
		return
	}
	count, err := h.Svc.UnreadCount(ctx, userID, tenantID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "count_failed", "failed to count notifications", err.Error())
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"unread": count})
}

// MarkRead marks a single notification as read.
func (h *Handler) MarkRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, tenantID, ok := identity(w, r)
	if !ok {
		return
	}
	id, err := toUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "invalid_id", "invalid notification id", err.Error())
		return
	}
	updated, err := h.Svc.MarkRead(ctx, id, userID, tenantID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		common.JSONError(w, http.StatusInternalServerError, "mark_failed", "failed to mark notification", err.Error())
		return
	}
	// A missing row (not found for this user, or already read) is an idempotent
	// no-op: the client's intent — "this notification is read" — holds either way.
	_ = updated
	common.JSON(w, http.StatusOK, map[string]bool{"read": true})
}

// MarkAllRead marks every unread notification for the user as read.
func (h *Handler) MarkAllRead(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID, tenantID, ok := identity(w, r)
	if !ok {
		return
	}
	if err := h.Svc.MarkAllRead(ctx, userID, tenantID); err != nil {
		common.JSONError(w, http.StatusInternalServerError, "mark_failed", "failed to mark notifications", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// identity resolves the authenticated user and tenant, writing the appropriate
// error response and returning ok=false when either is missing or malformed.
func identity(w http.ResponseWriter, r *http.Request) (userID, tenantID pgtype.UUID, ok bool) {
	ctx := r.Context()
	userIDStr, has := common.UserID(ctx)
	if !has {
		common.JSONError(w, http.StatusUnauthorized, "unauthorized", "unauthorized", nil)
		return userID, tenantID, false
	}
	var err error
	userID, err = toUUID(userIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "invalid_user_id", "invalid user id", err.Error())
		return userID, tenantID, false
	}
	tenantIDStr, has := tenant.FromContext(ctx)
	if !has {
		common.JSONError(w, http.StatusBadRequest, "missing_tenant", "missing tenant context", nil)
		return userID, tenantID, false
	}
	tenantID, err = toUUID(tenantIDStr)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "invalid_tenant_id", "invalid tenant id", err.Error())
		return userID, tenantID, false
	}
	return userID, tenantID, true
}

func toDTO(n dbgen.Notification) dto {
	raw := json.RawMessage(n.Data)
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	d := dto{
		ID:        uuidString(n.ID),
		Type:      n.Type,
		Title:     n.Title,
		Body:      n.Body,
		Data:      raw,
		Read:      n.ReadAt.Valid,
		CreatedAt: n.CreatedAt.Time.Format(timeLayout),
	}
	if n.ReadAt.Valid {
		s := n.ReadAt.Time.Format(timeLayout)
		d.ReadAt = &s
	}
	return d
}

func toUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return uuid.UUID(id.Bytes).String()
}
