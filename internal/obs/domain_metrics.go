package obs

import (
	"fmt"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	domainOnce sync.Once

	// PaymentIntentTotal counts payment intent creation attempts.
	PaymentIntentTotal *prometheus.CounterVec
	// PaymentWebhookTotal counts inbound payment webhook processing outcomes.
	PaymentWebhookTotal *prometheus.CounterVec
	// ShippingWebhookTotal counts inbound shipping webhook processing outcomes.
	ShippingWebhookTotal *prometheus.CounterVec
	// WebhookDeliveriesTotal tracks webhook dispatch outcomes.
	WebhookDeliveriesTotal *prometheus.CounterVec
	// WebhookAttemptLatency records delivery attempt latency in milliseconds.
	WebhookAttemptLatency *prometheus.HistogramVec
	// WebhookDispatchAttempts counts dispatcher attempts regardless of outcome.
	WebhookDispatchAttempts prometheus.Counter
	// WebhookDispatchDLQ counts deliveries moved to dead-letter queue.
	WebhookDispatchDLQ prometheus.Counter
)

// MustRegisterDomainMetrics initialises and registers domain-specific Prometheus collectors.
func MustRegisterDomainMetrics(namespace string, reg prometheus.Registerer) {
	domainOnce.Do(func() {
		if reg == nil {
			reg = prometheus.DefaultRegisterer
		}
		PaymentIntentTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "payment_intent_total",
			Help:      "Count of payment intent processing outcomes.",
		}, []string{"provider", "channel", "result"})
		PaymentWebhookTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "payment_webhook_total",
			Help:      "Count of processed payment webhooks by outcome.",
		}, []string{"provider", "result"})
		ShippingWebhookTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "shipping_webhook_total",
			Help:      "Count of processed shipping webhooks by outcome.",
		}, []string{"courier", "result"})
		WebhookDeliveriesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "webhook_deliveries_total",
			Help:      "Count of webhook delivery outcomes.",
		}, []string{"result"})
		WebhookAttemptLatency = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: namespace,
			Name:      "webhook_attempt_duration_ms",
			Help:      "Latency for webhook delivery attempts in milliseconds.",
			Buckets:   []float64{10, 25, 50, 100, 250, 500, 1000, 2500, 5000},
		}, []string{"result"})
		WebhookDispatchAttempts = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "webhook_dispatch_attempts_total",
			Help:      "Total number of webhook dispatch attempts.",
		})
		WebhookDispatchDLQ = prometheus.NewCounter(prometheus.CounterOpts{
			Namespace: namespace,
			Name:      "webhook_dispatch_dlq_total",
			Help:      "Number of webhook deliveries moved to the dead-letter queue.",
		})

		mustRegisterCollector(reg, PaymentIntentTotal, func(existing prometheus.Collector) {
			if v, ok := existing.(*prometheus.CounterVec); ok {
				PaymentIntentTotal = v
			}
		})
		mustRegisterCollector(reg, PaymentWebhookTotal, func(existing prometheus.Collector) {
			if v, ok := existing.(*prometheus.CounterVec); ok {
				PaymentWebhookTotal = v
			}
		})
		mustRegisterCollector(reg, ShippingWebhookTotal, func(existing prometheus.Collector) {
			if v, ok := existing.(*prometheus.CounterVec); ok {
				ShippingWebhookTotal = v
			}
		})
		mustRegisterCollector(reg, WebhookDeliveriesTotal, func(existing prometheus.Collector) {
			if v, ok := existing.(*prometheus.CounterVec); ok {
				WebhookDeliveriesTotal = v
			}
		})
		mustRegisterCollector(reg, WebhookAttemptLatency, func(existing prometheus.Collector) {
			if v, ok := existing.(*prometheus.HistogramVec); ok {
				WebhookAttemptLatency = v
			}
		})
		mustRegisterCollector(reg, WebhookDispatchAttempts, func(existing prometheus.Collector) {
			if v, ok := existing.(prometheus.Counter); ok {
				WebhookDispatchAttempts = v
			}
		})
		mustRegisterCollector(reg, WebhookDispatchDLQ, func(existing prometheus.Collector) {
			if v, ok := existing.(prometheus.Counter); ok {
				WebhookDispatchDLQ = v
			}
		})
	})
}

func mustRegisterCollector(reg prometheus.Registerer, collector prometheus.Collector, reuse func(prometheus.Collector)) {
	if err := reg.Register(collector); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if reuse != nil {
				reuse(are.ExistingCollector)
			}
			return
		}
		panic(fmt.Errorf("register domain metric: %w", err))
	}
}
