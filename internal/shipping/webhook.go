package shipping

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/obs"
)

type replayStore interface {
	SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd
}

// Webhook handles courier callbacks and synchronises shipment state.
type Webhook struct {
	Svc       *Service
	Replay    replayStore
	ReplayTTL time.Duration
}

type webhookPayload struct {
	OrderID        string     `json:"orderId"`
	TrackingNumber string     `json:"trackingNumber"`
	ExternalStatus string     `json:"externalStatus"`
	Description    *string    `json:"description"`
	Location       *string    `json:"location"`
	OccurredAt     *time.Time `json:"occurredAt"`
}

// Handle processes webhook callbacks from configured couriers.
func (h Webhook) Handle(w http.ResponseWriter, r *http.Request) {
	if h.Svc == nil || h.Svc.Q == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "shipment service not configured", nil)
		return
	}
	if h.Replay == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "replay protection not configured", nil)
		return
	}
	ctx, span := otel.Tracer("shipping.Webhook").Start(r.Context(), "ShippingWebhook.Handle")
	defer span.End()
	r = r.WithContext(ctx)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		span.RecordError(err)
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "unable to read payload", nil)
		return
	}
	courier := chi.URLParam(r, "courier")
	span.SetAttributes(attribute.String("shipping.webhook.courier", courier))
	courierLabel := normaliseLabel(courier)
	outcome := "error"
	defer func() {
		if obs.ShippingWebhookTotal != nil {
			obs.ShippingWebhookTotal.WithLabelValues(courierLabel, outcome).Inc()
		}
	}()
	key := fmt.Sprintf("shwh:%s:%s", courier, common.Sha256Hex(string(body)))
	ok, err := h.Replay.SetNX(r.Context(), key, "1", h.ReplayTTL).Result()
	if err != nil {
		span.RecordError(err)
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "replay protection failed", nil)
		return
	}
	if !ok {
		span.AddEvent("shipping webhook replay prevented")
		common.JSONError(w, http.StatusConflict, "REPLAY", "duplicate webhook payload", nil)
		return
	}
	payload, err := decodeWebhookPayload(body, r)
	if err != nil {
		span.RecordError(err)
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	orderID, err := parseUUID(payload.OrderID)
	if err != nil {
		span.RecordError(err)
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid order id", nil)
		return
	}
	span.SetAttributes(attribute.String("shipping.webhook.order_id", payload.OrderID))
	status := MapExternalToStatus(payload.ExternalStatus)
	if status == dbgen.ShipmentStatusPENDING {
		span.RecordError(errors.New("unrecognised external status"))
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "unrecognised external status", nil)
		return
	}
	span.SetAttributes(attribute.String("shipping.webhook.status", string(status)))
	if _, _, err := h.Svc.AppendEvent(r.Context(), orderID, status, payload.Description, payload.Location, payload.OccurredAt, body); err != nil {
		switch {
		case errors.Is(err, ErrInvalidShipmentTransition):
			span.RecordError(err)
			common.JSONError(w, http.StatusConflict, "INVALID_STATE", err.Error(), nil)
			return
		case errors.Is(err, pgx.ErrNoRows):
			span.RecordError(err)
			common.JSONError(w, http.StatusNotFound, "NOT_FOUND", "shipment not found", nil)
			return
		default:
			span.RecordError(err)
			common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "failed to record shipment event", nil)
			return
		}
	}
	outcome = "success"
	w.WriteHeader(http.StatusNoContent)
}

func decodeWebhookPayload(body []byte, r *http.Request) (webhookPayload, error) {
	var payload webhookPayload
	if len(body) > 0 {
		if err := json.Unmarshal(body, &payload); err != nil {
			payload = webhookPayload{}
		}
	}
	if payload.OrderID == "" {
		payload.OrderID = r.URL.Query().Get("orderId")
	}
	if payload.TrackingNumber == "" {
		payload.TrackingNumber = r.URL.Query().Get("tracking")
	}
	if payload.ExternalStatus == "" {
		payload.ExternalStatus = r.URL.Query().Get("status")
	}
	if payload.Description == nil {
		if desc := strings.TrimSpace(r.URL.Query().Get("description")); desc != "" {
			payload.Description = &desc
		}
	}
	if payload.Location == nil {
		if loc := strings.TrimSpace(r.URL.Query().Get("location")); loc != "" {
			payload.Location = &loc
		}
	}
	if payload.OccurredAt == nil {
		if ts := strings.TrimSpace(r.URL.Query().Get("occurredAt")); ts != "" {
			if parsed, err := time.Parse(time.RFC3339, ts); err == nil {
				payload.OccurredAt = &parsed
			}
		}
	}
	if payload.OrderID == "" {
		return webhookPayload{}, errors.New("orderId is required")
	}
	if payload.ExternalStatus == "" {
		return webhookPayload{}, errors.New("status is required")
	}
	return payload, nil
}

func normaliseLabel(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return "unknown"
	}
	return trimmed
}
