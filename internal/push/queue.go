package push

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/noah-isme/backend-toko/internal/queue"
)

const pushDeliveryTask = "push-delivery"

// PushDeliveryTask returns the queue kind used for web push deliveries.
func PushDeliveryTask() string {
	return pushDeliveryTask
}

// ErrQueueUnavailable is returned when no Redis client is configured.
var ErrQueueUnavailable = errors.New("push queue: redis client not configured")

// Task is the payload of a push-delivery task. It is opaque on purpose
// (queue_dlq is readable by every tenant's admin): the worker resolves the
// order, user and message from domain_events at send time.
type Task struct {
	EventID string `json:"eventId"`
	Topic   string `json:"topic"`
}

// TaskEnqueuer publishes push-delivery tasks.
type TaskEnqueuer interface {
	EnqueueTask(ctx context.Context, task Task, idempotencyKey string) error
}

// Enqueuer publishes push-delivery tasks on the Redis queue.
type Enqueuer struct {
	Queue       queue.Enqueuer
	MaxAttempts int
}

// EnqueueTask implements TaskEnqueuer.
func (e *Enqueuer) EnqueueTask(ctx context.Context, task Task, idempotencyKey string) error {
	if e == nil || e.Queue.R == nil {
		return ErrQueueUnavailable
	}
	payload, err := json.Marshal(task)
	if err != nil {
		return err
	}
	maxAttempts := e.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 8
	}
	return e.Queue.Enqueue(ctx, queue.Task{
		Kind:           pushDeliveryTask,
		Payload:        payload,
		IdempotencyKey: idempotencyKey,
		MaxAttempts:    maxAttempts,
	})
}
