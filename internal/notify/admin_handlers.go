package notify

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// AdminHandler exposes management endpoints for webhook configuration and monitoring.
//
// Endpoints belong to the request's tenant: they are created with its
// tenant_id (so Dispatcher.Schedule, which delivers an event only to the
// endpoints of the event's tenant, selects them) and list, update and delete
// never see another tenant's endpoints (another tenant's id reads as 404).
type AdminHandler struct {
	Store Store
	Disp  *Dispatcher
}

type endpointRequest struct {
	Name   string   `json:"name"`
	URL    string   `json:"url"`
	Secret string   `json:"secret"`
	Active *bool    `json:"active"`
	Topics []string `json:"topics"`
}

// CreateEndpoint registers a new webhook endpoint.
func (h *AdminHandler) CreateEndpoint(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "webhook store unavailable", nil)
		return
	}
	tenantID, ok := requestTenant(w, r)
	if !ok {
		return
	}
	var req endpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.URL) == "" || strings.TrimSpace(req.Secret) == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "name, url and secret are required", nil)
		return
	}
	if err := validateURL(req.URL); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	topics := normaliseTopics(req.Topics)
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	endpoint, err := h.Store.CreateWebhookEndpoint(r.Context(), dbgen.CreateWebhookEndpointParams{
		Name:     req.Name,
		Url:      req.URL,
		Secret:   req.Secret,
		Active:   active,
		Topics:   topics,
		TenantID: tenantID,
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	common.JSON(w, http.StatusCreated, endpoint)
}

// UpdateEndpoint updates an existing webhook endpoint.
func (h *AdminHandler) UpdateEndpoint(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "webhook store unavailable", nil)
		return
	}
	tenantID, ok := requestTenant(w, r)
	if !ok {
		return
	}
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid id", nil)
		return
	}
	var req endpointRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.URL) == "" || strings.TrimSpace(req.Secret) == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "name, url and secret are required", nil)
		return
	}
	if err := validateURL(req.URL); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	active := true
	if req.Active != nil {
		active = *req.Active
	}
	endpoint, err := h.Store.UpdateWebhookEndpoint(r.Context(), dbgen.UpdateWebhookEndpointParams{
		ID:       id,
		Name:     req.Name,
		Url:      req.URL,
		Secret:   req.Secret,
		Active:   active,
		Topics:   normaliseTopics(req.Topics),
		TenantID: tenantID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "webhook endpoint not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	common.JSON(w, http.StatusOK, endpoint)
}

// ListEndpoints returns configured webhook endpoints.
func (h *AdminHandler) ListEndpoints(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "webhook store unavailable", nil)
		return
	}
	tenantID, ok := requestTenant(w, r)
	if !ok {
		return
	}
	limit, offset := pagination(r)
	endpoints, err := h.Store.ListWebhookEndpoints(r.Context(), dbgen.ListWebhookEndpointsParams{
		TenantID:   tenantID,
		PageOffset: int32(offset),
		PageLimit:  int32(limit),
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": endpoints})
}

// DeleteEndpoint removes an endpoint by ID.
func (h *AdminHandler) DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "webhook store unavailable", nil)
		return
	}
	tenantID, ok := requestTenant(w, r)
	if !ok {
		return
	}
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid id", nil)
		return
	}
	deleted, err := h.Store.DeleteWebhookEndpoint(r.Context(), dbgen.DeleteWebhookEndpointParams{ID: id, TenantID: tenantID})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	if deleted == 0 {
		common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "webhook endpoint not found", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListDeliveries returns webhook delivery attempts with optional filtering.
func (h *AdminHandler) ListDeliveries(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "webhook store unavailable", nil)
		return
	}
	endpointID, _ := parseUUIDOptional(r.URL.Query().Get("endpointId"))
	eventID, _ := parseUUIDOptional(r.URL.Query().Get("eventId"))
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit, offset := pagination(r)
	rows, err := h.Store.ListWebhookDeliveries(r.Context(), dbgen.ListWebhookDeliveriesParams{
		EndpointID: endpointID,
		EventID:    eventID,
		Status:     status,
		PageOffset: int32(offset),
		PageLimit:  int32(limit),
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	total, err := h.Store.CountWebhookDeliveries(r.Context(), dbgen.CountWebhookDeliveriesParams{
		EndpointID: endpointID,
		EventID:    eventID,
		Status:     status,
	})
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{"data": rows, "total": total})
}

// ReplayDelivery resets a delivery for retry.
func (h *AdminHandler) ReplayDelivery(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "webhook store unavailable", nil)
		return
	}
	id, err := parseUUID(chi.URLParam(r, "id"))
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid id", nil)
		return
	}
	delivery, err := h.Store.ResetDeliveryForReplay(r.Context(), id)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, pgx.ErrNoRows) {
			status = http.StatusNotFound
		}
		common.JSONError(w, status, "INTERNAL", err.Error(), nil)
		return
	}
	_ = h.Store.DeleteDlqByDelivery(r.Context(), id)
	if h.Disp != nil && h.Disp.Replay != nil {
		_ = h.Disp.Replay.Release(r.Context(), replayKey(delivery.EndpointID, delivery.EventID))
	}
	common.JSON(w, http.StatusOK, delivery)
}

func normaliseTopics(topics []string) []string {
	seen := make(map[string]struct{}, len(topics))
	result := make([]string, 0, len(topics))
	for _, topic := range topics {
		trimmed := strings.TrimSpace(strings.ToLower(topic))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return []string{}
	}
	return result
}

func pagination(r *http.Request) (limit, offset int) {
	limit = 50
	offset = 0
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("offset")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	return
}

// requestTenant returns the tenant resolved for the request, or writes
// 400 MISSING_TENANT. Webhook endpoints are tenant-owned, so the endpoint
// handlers never run without one.
func requestTenant(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
	tenantID, ok := tenantFromContext(r.Context())
	if !ok {
		common.JSONError(w, http.StatusBadRequest, "MISSING_TENANT", "tenant is required", nil)
		return pgtype.UUID{}, false
	}
	return tenantID, true
}

func parseUUID(value string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func parseUUIDOptional(value string) (pgtype.UUID, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return pgtype.UUID{}, nil
	}
	return parseUUID(value)
}
