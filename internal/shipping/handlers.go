package shipping

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Handler exposes HTTP endpoints for shipment creation and retrieval.
type Handler struct {
	Svc *Service
	Q   *dbgen.Queries
}

// GetByOrder returns shipment metadata and tracking events for the authenticated user.
func (h *Handler) GetByOrder(w http.ResponseWriter, r *http.Request) {
	if h.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "shipment queries not configured", nil)
		return
	}
	userID, ok := common.UserID(r.Context())
	if !ok || userID == "" {
		common.JSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "authentication required", nil)
		return
	}
	orderID := chi.URLParam(r, "orderId")
	oID, err := parseUUID(orderID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid order id", nil)
		return
	}
	uID, err := parseUUID(userID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil)
		return
	}
	order, err := h.Q.GetOrderByIDForUser(r.Context(), dbgen.GetOrderByIDForUserParams{ID: oID, UserID: uID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load order", nil)
		return
	}
	shipment, err := h.Q.GetShipmentByOrder(r.Context(), order.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "shipment not found", nil)
			return
		}
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load shipment", nil)
		return
	}
	events, err := h.Q.ListShipmentEvents(r.Context(), shipment.ID)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to load shipment events", nil)
		return
	}
	common.JSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"id":             uuidString(shipment.ID),
			"orderId":        uuidString(shipment.OrderID),
			"status":         shipment.Status,
			"courier":        nullableText(shipment.Courier),
			"trackingNumber": nullableText(shipment.TrackingNumber),
			"lastStatus":     resolveStatus(shipment.Status, shipment.LastStatus),
			"lastEventAt":    nullableTime(shipment.LastEventAt),
			"events":         serialiseEvents(events),
		},
	})
}

type createShipmentRequest struct {
	Courier        string `json:"courier"`
	TrackingNumber string `json:"trackingNumber"`
}

// AdminCreate allows administrators to register courier and tracking data for an order.
func (h *Handler) AdminCreate(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "shipment service not configured", nil)
		return
	}
	orderID := chi.URLParam(r, "id")
	oID, err := parseUUID(orderID)
	if err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid order id", nil)
		return
	}
	var req createShipmentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	shipment, err := h.Svc.Create(r.Context(), oID, req.Courier, req.TrackingNumber)
	if err != nil {
		switch {
		case errors.Is(err, ErrOrderNotEligible):
			common.JSONError(w, http.StatusConflict, "INVALID_STATE", err.Error(), nil)
			return
		case errors.Is(err, ErrShipmentAlreadyExists):
			common.JSONError(w, http.StatusConflict, "ALREADY_EXISTS", err.Error(), nil)
			return
		case errors.Is(err, pgx.ErrNoRows):
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "order not found", nil)
			return
		default:
			common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to create shipment", nil)
			return
		}
	}
	common.JSON(w, http.StatusCreated, map[string]any{
		"data": map[string]any{
			"id":             uuidString(shipment.ID),
			"orderId":        uuidString(shipment.OrderID),
			"status":         shipment.Status,
			"courier":        nullableText(shipment.Courier),
			"trackingNumber": nullableText(shipment.TrackingNumber),
			"lastStatus":     resolveStatus(shipment.Status, shipment.LastStatus),
			"lastEventAt":    nullableTime(shipment.LastEventAt),
			"events":         []any{},
		},
	})
}

func serialiseEvents(events []dbgen.ShipmentEvent) []map[string]any {
	result := make([]map[string]any, 0, len(events))
	for _, ev := range events {
		result = append(result, map[string]any{
			"id":          uuidString(ev.ID),
			"status":      ev.Status,
			"description": nullableText(ev.Description),
			"location":    nullableText(ev.Location),
			"occurredAt":  nullableTime(ev.OccurredAt),
		})
	}
	return result
}

func resolveStatus(status dbgen.ShipmentStatus, lastStatus dbgen.NullShipmentStatus) dbgen.ShipmentStatus {
	if lastStatus.Valid {
		return lastStatus.ShipmentStatus
	}
	return status
}

func nullableText(v pgtype.Text) *string {
	if !v.Valid {
		return nil
	}
	s := v.String
	return &s
}

func nullableTime(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	return &v.Time
}
