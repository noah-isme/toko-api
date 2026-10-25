package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

// EmailNotifier turns domain events into email-delivery tasks. It never sends
// mail itself: the task carries only the event id and topic, and EmailWorker
// renders and sends it from cmd/worker with retries and a dead-letter queue.
type EmailNotifier struct {
	Queue        EmailEnqueuer
	Enabled      bool
	From         string
	TopicToggles map[string]bool
}

// Notify implements the events.Notifier interface.
func (n EmailNotifier) Notify(ctx context.Context, event dbgen.DomainEvent) error {
	if !n.Enabled || n.Queue == nil {
		return nil
	}
	if !topicEnabled(n.TopicToggles, event.Topic) {
		return nil
	}
	if _, _, _, ok := RenderEmail(event); !ok {
		// No recipient in the payload: nothing to deliver.
		return nil
	}
	eventID := uuidFrom(event.ID)
	if eventID == "" {
		return errors.New("email notify: event id is required")
	}
	return n.Queue.EnqueueEmail(ctx, EmailTask{EventID: eventID, Topic: event.Topic}, "email:"+eventID)
}

// RenderEmail builds the transactional email for an event. ok is false when the
// payload names no recipient (or cannot be decoded).
func RenderEmail(event dbgen.DomainEvent) (to, subject, body string, ok bool) {
	payload := map[string]any{}
	if len(event.Payload) > 0 {
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return "", "", "", false
		}
	}
	to = extractRecipient(payload)
	if to == "" {
		return "", "", "", false
	}
	return to, subjectFor(event.Topic), bodyFor(event.Topic, payload, event.OccurredAt.Time), true
}

// MergeShipmentToggles folds the NOTIFY_ON_* shipment flags into the per-topic
// email toggles: a shipment email is sent only when its topic toggle (default
// on) and its NOTIFY_ON_* flag are both enabled. The input map is not modified.
func MergeShipmentToggles(toggles map[string]bool, onShipped, onOutForDelivery, onDelivered bool) map[string]bool {
	merged := make(map[string]bool, len(toggles)+3)
	for topic, enabled := range toggles {
		merged[topic] = enabled
	}
	for topic, flag := range map[string]bool{
		events.TopicShipmentShipped:        onShipped,
		events.TopicShipmentOutForDelivery: onOutForDelivery,
		events.TopicShipmentDelivered:      onDelivered,
	} {
		merged[topic] = topicEnabled(toggles, topic) && flag
	}
	return merged
}

// topicEnabled reports whether a topic is enabled; topics absent from the map
// are enabled.
func topicEnabled(toggles map[string]bool, topic string) bool {
	if toggles == nil {
		return true
	}
	enabled, ok := toggles[topic]
	return !ok || enabled
}

func extractRecipient(payload map[string]any) string {
	keys := []string{"email", "recipient", "userEmail", "customerEmail"}
	for _, key := range keys {
		if val, ok := payload[key]; ok {
			if s, ok := val.(string); ok {
				s = strings.TrimSpace(s)
				if s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func subjectFor(topic string) string {
	switch topic {
	case events.TopicOrderCreated:
		return "Pesanan diterima"
	case events.TopicOrderPaid:
		return "Pembayaran berhasil"
	case events.TopicOrderCanceled:
		return "Pesanan dibatalkan"
	case events.TopicPaymentFailed:
		return "Pembayaran gagal"
	case events.TopicPaymentExpired:
		return "Pembayaran kedaluwarsa"
	case events.TopicShipmentShipped:
		return "Pesanan dikirim"
	case events.TopicShipmentOutForDelivery:
		return "Pesanan dalam perjalanan"
	case events.TopicShipmentDelivered:
		return "Pesanan telah diterima"
	default:
		return fmt.Sprintf("Notifikasi %s", topic)
	}
}

// shipmentLead is the customer-facing first line for shipment emails (the copy
// previously sent inline by the shipping service).
var shipmentLead = map[string]string{
	events.TopicShipmentShipped:        "Pesanan Anda telah dikirim.",
	events.TopicShipmentOutForDelivery: "Pesanan Anda sedang dalam perjalanan.",
	events.TopicShipmentDelivered:      "Pesanan Anda telah diterima.",
}

func bodyFor(topic string, payload map[string]any, occurred time.Time) string {
	summary := fmt.Sprintf("Event %s terjadi pada %s.", topic, occurred.Format(time.RFC3339))
	if lead, ok := shipmentLead[topic]; ok {
		summary = lead + "\n" + summary
	}
	if orderID, ok := payload["orderId"].(string); ok && orderID != "" {
		summary += fmt.Sprintf("\nID Pesanan: %s", orderID)
	}
	if shipmentID, ok := payload["shipmentId"].(string); ok && shipmentID != "" {
		summary += fmt.Sprintf("\nID Pengiriman: %s", shipmentID)
	}
	if note, ok := payload["message"].(string); ok && note != "" {
		summary += "\n" + note
	}
	return summary
}
