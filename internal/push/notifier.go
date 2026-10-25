package push

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/events"
)

// Notifier enqueues a push-delivery task for each event that has push copy.
// It implements events.Notifier and does no database work on the request path.
type Notifier struct {
	Enqueue TaskEnqueuer
	Enabled bool
}

// Notify implements events.Notifier.
func (n *Notifier) Notify(ctx context.Context, event dbgen.DomainEvent) error {
	if n == nil || !n.Enabled || n.Enqueue == nil {
		return nil
	}
	if _, ok := templates[event.Topic]; !ok {
		return nil
	}
	eventID := uuidString(event.ID)
	if eventID == "" {
		return errors.New("push notify: event id is required")
	}
	return n.Enqueue.EnqueueTask(ctx, Task{EventID: eventID, Topic: event.Topic}, "push:"+eventID)
}

type template struct {
	title string
	body  string
}

// templates holds push copy per topic (same wording as the in-app
// notifications). order.created is not pushed.
var templates = map[string]template{
	events.TopicOrderPaid:              {title: "Pembayaran diterima", body: "Pembayaran pesanan Anda telah kami terima."},
	events.TopicOrderCanceled:          {title: "Pesanan dibatalkan", body: "Pesanan Anda telah dibatalkan."},
	events.TopicPaymentFailed:          {title: "Pembayaran gagal", body: "Pembayaran pesanan Anda gagal diproses."},
	events.TopicPaymentExpired:         {title: "Pembayaran kedaluwarsa", body: "Batas waktu pembayaran pesanan Anda telah berakhir."},
	events.TopicShipmentShipped:        {title: "Pesanan dikirim", body: "Pesanan Anda telah dikirim."},
	events.TopicShipmentOutForDelivery: {title: "Pesanan dalam pengiriman", body: "Kurir sedang mengantar pesanan Anda."},
	events.TopicShipmentDelivered:      {title: "Pesanan tiba", body: "Pesanan Anda telah sampai di tujuan."},
}

// BuildMessage renders the push for an event about orderID. It returns the
// preference category and false when the topic has no push copy.
func BuildMessage(event dbgen.DomainEvent, orderID pgtype.UUID) (Message, string, bool) {
	tmpl, ok := templates[event.Topic]
	if !ok || !orderID.Valid {
		return Message{}, "", false
	}
	id := uuidString(orderID)
	return Message{
		Title: tmpl.title,
		Body:  tmpl.body,
		Tag:   "order:" + id,
		Data: map[string]any{
			"url":     "/account/orders/" + id,
			"orderId": id,
			"topic":   event.Topic,
		},
	}, CategoryOrderUpdate, true
}
