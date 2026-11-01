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

// IsCustomerSilent reports whether a topic is an internal/ops signal that must
// never produce a customer notification, whatever its payload carries.
func IsCustomerSilent(topic string) bool {
	return topic == TopicPaymentLate || topic == TopicPaymentProofUnreviewed
}
