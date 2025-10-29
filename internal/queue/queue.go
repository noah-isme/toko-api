package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/noah-isme/backend-toko/internal/resilience"
)

// Task represents a job to be processed asynchronously.
type Task struct {
	Kind           string
	Payload        []byte
	IdempotencyKey string
	MaxAttempts    int
	Delay          time.Duration
}

// Enqueuer publishes tasks to Redis backed queues.
type Enqueuer struct {
	R        *redis.Client
	Prefix   string
	DedupTTL time.Duration
}

// Enqueue inserts the task into the queue. If an idempotency key is supplied the
// task is only enqueued once within the configured deduplication window.
func (e Enqueuer) Enqueue(ctx context.Context, t Task) error {
	if e.R == nil {
		return errors.New("queue: redis client not configured")
	}
	kind := sanitizeKind(t.Kind)
	if kind == "" {
		return errors.New("queue: task kind is required")
	}
	msg := taskMessage{
		Kind:        kind,
		Key:         t.IdempotencyKey,
		Payload:     t.Payload,
		Attempt:     0,
		MaxAttempts: t.MaxAttempts,
	}
	if msg.MaxAttempts <= 0 {
		msg.MaxAttempts = 10
	}
	availableAt := time.Now().Add(t.Delay)
	msg.AvailableAt = availableAt.UnixNano()

	if msg.Key != "" {
		dedupKey := e.dedupKey(kind, msg.Key)
		ttl := e.DedupTTL
		if ttl <= 0 {
			ttl = 24 * time.Hour
		}
		ok, err := e.R.SetNX(ctx, dedupKey, "1", ttl).Result()
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
	}

	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	queueKey := e.queueKey(kind)
	score := float64(msg.AvailableAt)
	return e.R.ZAdd(ctx, queueKey, redis.Z{Score: score, Member: raw}).Err()
}

func (e Enqueuer) queueKey(kind string) string {
	if e.Prefix == "" {
		return fmt.Sprintf("queue:%s", kind)
	}
	return fmt.Sprintf("%s:queue:%s", e.Prefix, kind)
}

func (e Enqueuer) dedupKey(kind, key string) string {
	if e.Prefix == "" {
		return fmt.Sprintf("queue:dedup:%s:%s", kind, key)
	}
	return fmt.Sprintf("%s:dedup:%s:%s", e.Prefix, kind, key)
}

func sanitizeKind(kind string) string {
	for i := 0; i < len(kind); i++ {
		c := kind[i]
		if c >= 'a' && c <= 'z' {
			continue
		}
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '-' || c == '_' || c == ':' {
			continue
		}
		return ""
	}
	return kind
}

// Worker consumes tasks for a specific kind.
type Worker struct {
	R                 *redis.Client
	Prefix            string
	Kind              string
	Concurrency       int
	VisibilityTimeout time.Duration
	Handler           func(context.Context, Task) error
	RetryBase         time.Duration
	RetryJitter       float64
}

// Run starts processing tasks until the context is cancelled. Active tasks are
// tracked in a processing set to enable redelivery when workers crash.
func (w Worker) Run(ctx context.Context) error {
	if w.R == nil {
		return errors.New("queue: worker redis client not configured")
	}
	if w.Handler == nil {
		return errors.New("queue: worker handler not configured")
	}
	kind := sanitizeKind(w.Kind)
	if kind == "" {
		return errors.New("queue: worker kind is required")
	}
	concurrency := w.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	visibility := w.VisibilityTimeout
	if visibility <= 0 {
		visibility = 30 * time.Second
	}

	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	processingKey := w.processingKey(kind)
	queueKey := w.queueKey(kind)
	retryBase := w.RetryBase
	if retryBase <= 0 {
		retryBase = 200 * time.Millisecond
	}

	requeueTicker := time.NewTicker(time.Second)
	defer requeueTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return nil
		case <-requeueTicker.C:
			if err := w.requeueExpired(ctx, processingKey, queueKey); err != nil {
				return err
			}
		default:
		}

		res, err := w.R.ZPopMin(ctx, queueKey, 1).Result()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			if err == redis.Nil {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return err
		}
		if len(res) == 0 {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		member, ok := res[0].Member.(string)
		if !ok {
			continue
		}
		msg, err := decodeMessage(member)
		if err != nil {
			continue
		}
		now := time.Now().UnixNano()
		if msg.AvailableAt > now {
			// not due yet, push back and wait
			w.R.ZAdd(ctx, queueKey, redis.Z{Score: float64(msg.AvailableAt), Member: member})
			sleep := time.Duration(msg.AvailableAt-now) * time.Nanosecond
			if sleep > time.Second {
				sleep = time.Second
			}
			time.Sleep(sleep)
			continue
		}

		msg.Attempt++
		rawBytes, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		raw := string(rawBytes)
		deadline := time.Now().Add(visibility).UnixNano()
		if err := w.R.ZAdd(ctx, processingKey, redis.Z{Score: float64(deadline), Member: raw}).Err(); err != nil {
			return err
		}

		sem <- struct{}{}
		wg.Add(1)
		go func(raw string, m taskMessage) {
			defer func() { <-sem }()
			defer wg.Done()
			jobCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			err := w.Handler(jobCtx, Task{Kind: kind, Payload: m.Payload, IdempotencyKey: m.Key})
			if err != nil {
				w.handleFailure(jobCtx, queueKey, processingKey, raw, m, retryBase)
				return
			}
			w.ack(jobCtx, processingKey, raw, m)
		}(raw, msg)
	}
}

func (w Worker) handleFailure(ctx context.Context, queueKey, processingKey, raw string, msg taskMessage, base time.Duration) {
	if raw != "" {
		_ = w.R.ZRem(ctx, processingKey, raw)
	}
	if msg.MaxAttempts > 0 && msg.Attempt >= msg.MaxAttempts {
		dlqKey := w.dlqKey(msg.Kind)
		rawBytes, err := json.Marshal(msg)
		if err != nil {
			return
		}
		_ = w.R.LPush(ctx, dlqKey, rawBytes).Err()
		if msg.Key != "" {
			_ = w.R.Del(ctx, w.dedupKey(msg.Kind, msg.Key)).Err()
		}
		return
	}
	delay := resilience.Backoff(base, msg.Attempt, w.RetryJitter)
	msg.AvailableAt = time.Now().Add(delay).UnixNano()
	rawBytes, err := json.Marshal(msg)
	if err != nil {
		return
	}
	_ = w.R.ZAdd(ctx, queueKey, redis.Z{Score: float64(msg.AvailableAt), Member: string(rawBytes)}).Err()
}

func (w Worker) ack(ctx context.Context, processingKey, raw string, msg taskMessage) {
	if raw != "" {
		_ = w.R.ZRem(ctx, processingKey, raw)
	}
	if msg.Key != "" {
		_ = w.R.Del(ctx, w.dedupKey(msg.Kind, msg.Key)).Err()
	}
}

func (w Worker) requeueExpired(ctx context.Context, processingKey, queueKey string) error {
	now := float64(time.Now().UnixNano())
	due, err := w.R.ZRangeByScore(ctx, processingKey, &redis.ZRangeBy{Min: "-inf", Max: fmt.Sprintf("%f", now)}).Result()
	if err != nil && err != redis.Nil {
		return err
	}
	for _, raw := range due {
		msg, err := decodeMessage(raw)
		if err != nil {
			continue
		}
		_ = w.R.ZRem(ctx, processingKey, raw).Err()
		msg.AvailableAt = time.Now().UnixNano()
		encoded, err := json.Marshal(msg)
		if err != nil {
			continue
		}
		_ = w.R.ZAdd(ctx, queueKey, redis.Z{Score: float64(msg.AvailableAt), Member: encoded}).Err()
	}
	return nil
}

func (w Worker) queueKey(kind string) string {
	if w.Prefix == "" {
		return fmt.Sprintf("queue:%s", kind)
	}
	return fmt.Sprintf("%s:queue:%s", w.Prefix, kind)
}

func (w Worker) processingKey(kind string) string {
	if w.Prefix == "" {
		return fmt.Sprintf("queue:%s:processing", kind)
	}
	return fmt.Sprintf("%s:%s:processing", w.Prefix, kind)
}

func (w Worker) dlqKey(kind string) string {
	if w.Prefix == "" {
		return fmt.Sprintf("queue:%s:dlq", kind)
	}
	return fmt.Sprintf("%s:%s:dlq", w.Prefix, kind)
}

func (w Worker) dedupKey(kind, key string) string {
	if w.Prefix == "" {
		return fmt.Sprintf("queue:dedup:%s:%s", kind, key)
	}
	return fmt.Sprintf("%s:dedup:%s:%s", w.Prefix, kind, key)
}

func decodeMessage(raw string) (taskMessage, error) {
	var msg taskMessage
	if err := json.Unmarshal([]byte(raw), &msg); err != nil {
		return taskMessage{}, err
	}
	return msg, nil
}

type taskMessage struct {
	Kind        string `json:"kind"`
	Key         string `json:"key,omitempty"`
	Payload     []byte `json:"payload"`
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"max_attempts"`
	AvailableAt int64  `json:"available_at"`
}
