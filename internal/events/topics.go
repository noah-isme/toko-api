package events

// Topic constants for domain events emitted by the platform.
const (
	TopicOrderCreated   = "order.created"
	TopicOrderPaid      = "order.paid"
	TopicOrderCanceled  = "order.canceled"
	TopicPaymentFailed  = "payment.failed"
	TopicPaymentExpired = "payment.expired"
	// TopicPaymentLate is emitted when a payment is captured for an order that
	// already expired or was cancelled. It is an ops signal (manual refund) and
	// deliberately not in DefaultTopics: it must never reach the customer, so
	// its payload carries no email.
	TopicPaymentLate = "payment.late"
	// TopicPaymentProofUnreviewed is emitted when the checkout-expiry job
	// expires an order whose customer uploaded a transfer proof that no admin
	// confirmed: the money was probably sent and needs review or a refund. Ops
	// only, like payment.late; its payload carries no email.
	TopicPaymentProofUnreviewed = "payment.proof_unreviewed"
	TopicShipmentShipped        = "shipment.shipped"
	TopicShipmentOutForDelivery = "shipment.out_for_delivery"
	TopicShipmentDelivered      = "shipment.delivered"
	// Alert topics are emitted only on the dedicated alerts bus (no webhook
	// scheduler); see internal/alerts.
	TopicAlertBackInStock = "alert.back_in_stock"
	TopicAlertPriceDrop   = "alert.price_drop"

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
		TopicAlertBackInStock,
		TopicAlertPriceDrop,
	}
}

// RelayTopics are the topics the outbox relay re-dispatches: DefaultTopics
// without the alert topics, plus the catalog (product.*) topics, whose only
// consumers are tenant-scoped webhook endpoints. Alert events are inserted by internal/alerts in
// its own transaction and delivered through their own durable outbox
// (product_alert_deliveries) on a bus with only the email notifier; the
// relay's bus would also schedule webhooks (alert payloads carry customer
// PII) and write a second in-app notification.
func RelayTopics() []string {
	out := make([]string, 0, len(DefaultTopics()))
	for _, topic := range DefaultTopics() {
		if topic == TopicAlertBackInStock || topic == TopicAlertPriceDrop {
			continue
		}
		out = append(out, topic)
	}
	return append(out, TopicProductPriceChanged, TopicProductStockChanged, TopicProductStatusChanged)
}

// IsCustomerSilent reports whether a topic is an internal/ops signal that must
// never produce a customer notification, whatever its payload carries.
func IsCustomerSilent(topic string) bool {
	return topic == TopicPaymentLate || topic == TopicPaymentProofUnreviewed
}
