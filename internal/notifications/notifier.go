package notifications

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

// OrderLookup resolves the order behind a domain event so a notification can be
// addressed to the owning user and tenant. Satisfied by *dbgen.Queries.
type OrderLookup interface {
	GetOrderByID(ctx context.Context, id pgtype.UUID) (dbgen.Order, error)
}

// Notifier turns order and shipment domain events into per-user in-app
// notifications. It implements events.Notifier and is registered on the bus.
type Notifier struct {
	Svc    *Service
	Orders OrderLookup
	// OnError, when set, is invoked for non-fatal persistence failures so the
	// caller can log without aborting event fan-out.
	OnError func(error)
}

// template describes the user-facing copy for a topic.
type template struct {
	kind  string
	title string
	body  string
}

// templates maps domain topics to notification copy. Topics absent here produce
// no in-app notification (e.g. order.created is surfaced elsewhere).
var templates = map[string]template{
	events.TopicOrderPaid:              {kind: "order_paid", title: "Pembayaran diterima", body: "Pembayaran pesanan Anda telah kami terima."},
	events.TopicOrderCanceled:          {kind: "order_canceled", title: "Pesanan dibatalkan", body: "Pesanan Anda telah dibatalkan."},
	events.TopicPaymentFailed:          {kind: "payment_failed", title: "Pembayaran gagal", body: "Pembayaran pesanan Anda gagal diproses."},
	events.TopicPaymentExpired:         {kind: "payment_expired", title: "Pembayaran kedaluwarsa", body: "Batas waktu pembayaran pesanan Anda telah berakhir."},
	events.TopicShipmentShipped:        {kind: "shipment_shipped", title: "Pesanan dikirim", body: "Pesanan Anda telah dikirim."},
	events.TopicShipmentOutForDelivery: {kind: "shipment_out_for_delivery", title: "Pesanan dalam pengiriman", body: "Kurir sedang mengantar pesanan Anda."},
	events.TopicShipmentDelivered:      {kind: "shipment_delivered", title: "Pesanan tiba", body: "Pesanan Anda telah sampai di tujuan."},
}

// Notify persists an in-app notification for the event's owning user. Topics
// without a template, or events whose order cannot be resolved, are skipped
// without error so unrelated fan-out is unaffected.
func (n *Notifier) Notify(ctx context.Context, event dbgen.DomainEvent) error {
	if n == nil || n.Svc == nil || n.Orders == nil {
		return nil
	}
	tmpl, ok := templates[event.Topic]
	if !ok {
		return nil
	}
	orderID, ok := n.resolveOrderID(event)
	if !ok {
		return nil
	}
	order, err := n.Orders.GetOrderByID(ctx, orderID)
	if err != nil {
		n.reportError(err)
		return nil
	}
	if !order.UserID.Valid || !order.TenantID.Valid {
		return nil
	}
	data, _ := json.Marshal(map[string]any{
		"orderId": uuidString(order.ID),
		"topic":   event.Topic,
	})
	if _, err := n.Svc.Create(ctx, order.UserID, order.TenantID, tmpl.kind, tmpl.title, tmpl.body, data); err != nil {
		n.reportError(err)
		return nil
	}
	return nil
}

// resolveOrderID finds the order id for an event. Order/payment events carry it
// as the aggregate id; shipment events aggregate on the shipment id but embed
// the order id in the payload.
func (n *Notifier) resolveOrderID(event dbgen.DomainEvent) (pgtype.UUID, bool) {
	switch event.Topic {
	case events.TopicShipmentShipped, events.TopicShipmentOutForDelivery, events.TopicShipmentDelivered:
		var payload struct {
			OrderID string `json:"orderId"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil || payload.OrderID == "" {
			return pgtype.UUID{}, false
		}
		id, err := toUUID(payload.OrderID)
		if err != nil {
			return pgtype.UUID{}, false
		}
		return id, true
	default:
		if !event.AggregateID.Valid {
			return pgtype.UUID{}, false
		}
		return event.AggregateID, true
	}
}

func (n *Notifier) reportError(err error) {
	if n.OnError != nil && err != nil {
		n.OnError(err)
	}
}
