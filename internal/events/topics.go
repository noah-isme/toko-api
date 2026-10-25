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

	// Catalog topics, emitted by the admin catalog handlers (single, bulk and
	// import paths) through Bus.EmitForTenant so webhook deliveries are scoped
	// to the owning tenant. They carry no customer data and are deliberately not
	// part of DefaultTopics (no notification templates or toggles).
	TopicProductPriceChanged  = "product.price_changed"
	TopicProductStockChanged  = "product.stock_changed"
	TopicProductStatusChanged = "product.status_changed"
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
	}
}
