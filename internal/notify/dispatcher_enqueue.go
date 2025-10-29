package notify

import (
	"context"
	"strings"
	"time"

	"github.com/noah-isme/backend-toko/internal/queue"
)

const webhookDeliveryTask = "webhook-delivery"

// EnqueueDelivery publishes a webhook delivery task for processing by the worker.
func (d Dispatcher) EnqueueDelivery(ctx context.Context, deliveryID string, delay time.Duration, maxAttempts int) error {
	if strings.TrimSpace(deliveryID) == "" {
		return nil
	}
	if d.Queue.R == nil {
		return nil
	}
	if maxAttempts <= 0 {
		maxAttempts = d.DefaultMaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 6
		}
	}
	task := queue.Task{
		Kind:           webhookDeliveryTask,
		Payload:        []byte(deliveryID),
		IdempotencyKey: deliveryID,
		MaxAttempts:    maxAttempts,
		Delay:          delay,
	}
	return d.Queue.Enqueue(ctx, task)
}

// WebhookDeliveryTask returns the queue kind used for webhook deliveries.
func WebhookDeliveryTask() string {
	return webhookDeliveryTask
}
