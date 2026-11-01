package notify

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/noah-isme/backend-toko/internal/queue"
)

const emailDeliveryTask = "email-delivery"

// EmailDeliveryTask returns the queue kind used for transactional emails.
func EmailDeliveryTask() string {
	return emailDeliveryTask
}

// ErrEmailQueueUnavailable is returned when no Redis client is configured.
var ErrEmailQueueUnavailable = errors.New("email queue: redis client not configured")

// EmailTask is the payload of an email-delivery task. It is deliberately
// opaque: queue_dlq is global and readable by every tenant's admin, so the task
// names only the event; the worker loads the recipient, subject and body from
// domain_events at send time.
type EmailTask struct {
	EventID string `json:"eventId"`
	Topic   string `json:"topic"`
}

// EmailEnqueuer publishes email-delivery tasks.
type EmailEnqueuer interface {
	EnqueueEmail(ctx context.Context, task EmailTask, idempotencyKey string) error
}

// EmailQueue publishes email-delivery tasks on the Redis queue.
type EmailQueue struct {
	Queue       queue.Enqueuer
	MaxAttempts int
}

// EnqueueEmail implements EmailEnqueuer. The idempotency key deduplicates
// re-dispatches of the same event while the task is pending.
func (q *EmailQueue) EnqueueEmail(ctx context.Context, task EmailTask, idempotencyKey string) error {
	if q == nil || q.Queue.R == nil {
		return ErrEmailQueueUnavailable
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	maxAttempts := q.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	return q.Queue.Enqueue(ctx, queue.Task{
		Kind:           emailDeliveryTask,
		Payload:        payload,
		IdempotencyKey: idempotencyKey,
		MaxAttempts:    maxAttempts,
	})
}
