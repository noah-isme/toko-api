package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/noah-isme/backend-toko/internal/events"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// EmailNotifier sends transactional emails for selected topics.
type EmailNotifier struct {
	Mail         common.EmailSender
	Enabled      bool
	From         string
	TopicToggles map[string]bool
}

// Notify implements the events.Notifier interface.
func (n EmailNotifier) Notify(_ context.Context, event dbgen.DomainEvent) error {
	if !n.Enabled || n.Mail == nil {
		return nil
	}
	if n.TopicToggles != nil {
		if enabled, ok := n.TopicToggles[event.Topic]; ok && !enabled {
			return nil
		}
	}
	payload := map[string]any{}
	if len(event.Payload) > 0 {
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("email notify: decode payload: %w", err)
		}
	}
	to := extractRecipient(payload)
	if to == "" {
		return nil
	}
	subject := subjectFor(event.Topic)
	body := bodyFor(event.Topic, payload, event.OccurredAt.Time)
	return n.Mail.Send(to, subject, body)
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

func bodyFor(topic string, payload map[string]any, occurred time.Time) string {
	summary := fmt.Sprintf("Event %s terjadi pada %s.", topic, occurred.Format(time.RFC3339))
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
