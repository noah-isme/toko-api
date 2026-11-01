package events

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// OrderIDFor finds the order behind an event. Order and payment events carry it
// as the aggregate id; shipment events aggregate on the shipment id and embed
// the order id in the payload as "orderId".
func OrderIDFor(event dbgen.DomainEvent) (pgtype.UUID, bool) {
	switch event.Topic {
	case TopicShipmentShipped, TopicShipmentOutForDelivery, TopicShipmentDelivered:
		var payload struct {
			OrderID string `json:"orderId"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.OrderID == "" {
			return pgtype.UUID{}, false
		}
		parsed, err := uuid.Parse(payload.OrderID)
		if err != nil {
			return pgtype.UUID{}, false
		}
		return pgtype.UUID{Bytes: parsed, Valid: true}, true
	default:
		if !event.AggregateID.Valid {
			return pgtype.UUID{}, false
		}
		return event.AggregateID, true
	}
}

// uuidString renders a pgtype.UUID in canonical form ("" when invalid).
func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	b := id.Bytes
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
