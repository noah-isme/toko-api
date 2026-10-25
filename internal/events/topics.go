package events

// Topic constants for domain events emitted by the platform.
const (
	TopicOrderCreated           = "order.created"
	TopicOrderPaid              = "order.paid"
	TopicOrderCanceled          = "order.canceled"
	TopicPaymentFailed          = "payment.failed"
	TopicPaymentExpired         = "payment.expired"
	TopicShipmentShipped        = "shipment.shipped"
	TopicShipmentOutForDelivery = "shipment.out_for_delivery"
	TopicShipmentDelivered      = "shipment.delivered"
	// Alert topics are emitted only on the dedicated alerts bus (no webhook
	// scheduler); see internal/alerts.
	TopicAlertBackInStock = "alert.back_in_stock"
	TopicAlertPriceDrop   = "alert.price_drop"
)

// DefaultTopics returns the canonical list of topics that support notifications.
func DefaultTopics() []string {
	return []string{
		TopicOrderCreated,
		TopicOrderPaid,
		TopicOrderCanceled,
		TopicPaymentFailed,
		TopicPaymentExpired,
		TopicShipmentShipped,
		TopicShipmentOutForDelivery,
		TopicShipmentDelivered,
		TopicAlertBackInStock,
		TopicAlertPriceDrop,
	}
}
