package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/resilience"
)

var nopLogger = zerolog.Nop()

// Task represents a job to be processed asynchronously.
type Task struct {
	Kind           string
	Payload        []byte
	IdempotencyKey string
	MaxAttempts    int
	Attempt        int
	Delay          time.Duration
}

// Enqueuer publishes tasks to Redis backed queues.
type Enqueuer struct {
	R           *redis.Client
	Prefix      string
	DedupTTL    time.Duration
	MaxAttempts int
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
		Attempt:     t.Attempt,
		MaxAttempts: t.MaxAttempts,
	}
	if msg.Attempt < 0 {
		msg.Attempt = 0
	}
	if msg.MaxAttempts <= 0 {
		if e.MaxAttempts > 0 {
			msg.MaxAttempts = e.MaxAttempts
		} else {
			msg.MaxAttempts = 10
		}
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
	if err := e.R.ZAdd(ctx, queueKey, redis.Z{Score: score, Member: raw}).Err(); err != nil {
		return err
	}
	if QueueDepth != nil {
		if depth, err := e.R.ZCard(ctx, queueKey).Result(); err == nil {
			QueueDepth.WithLabelValues(kind).Set(float64(depth))
		}
	}
	return nil
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
	Store             Store
	HeartbeatInterval time.Duration
	SoftDeadline      time.Duration
	Logger            *zerolog.Logger
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
	heartbeat := w.HeartbeatInterval
	if heartbeat <= 0 {
		heartbeat = 5 * time.Second
	}
	softDeadline := w.SoftDeadline
	if softDeadline <= 0 || softDeadline >= visibility {
		margin := visibility / 4
		if margin <= 0 {
			margin = time.Second
		}
		softDeadline = visibility - margin
		if softDeadline <= 0 {
			softDeadline = visibility / 2
			if softDeadline <= 0 {
				softDeadline = visibility
			}
		}
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
	heartbeatTicker := time.NewTicker(heartbeat)
	defer heartbeatTicker.Stop()

	logger := w.logger().With().Str("queue_kind", kind).Logger()

	for {
		select {
		case <-ctx.Done():
			logger.Info().Msg("worker shutdown initiated")
			wg.Wait()
			_ = w.requeueExpired(context.Background(), processingKey, queueKey)
			w.updateDepth(context.Background(), queueKey, kind)
			w.updateDLQSize(context.Background(), kind)
			return nil
		case <-requeueTicker.C:
			if err := w.requeueExpired(ctx, processingKey, queueKey); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					continue
				}
				logger.Error().Err(err).Msg("requeue expired jobs failed")
				return err
			}
			w.updateDepth(ctx, queueKey, kind)
		case <-heartbeatTicker.C:
			w.updateDepth(ctx, queueKey, kind)
			w.updateDLQSize(ctx, kind)
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
			_ = w.R.ZAdd(ctx, queueKey, redis.Z{Score: float64(msg.AvailableAt), Member: member})
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

		w.updateDepth(ctx, queueKey, kind)

		sem <- struct{}{}
		wg.Add(1)
		go func(raw string, m taskMessage) {
			defer func() { <-sem }()
			defer wg.Done()
			jobCtx, cancel := context.WithTimeout(ctx, softDeadline)
			defer cancel()
			task := Task{Kind: kind, Payload: m.Payload, IdempotencyKey: m.Key, MaxAttempts: m.MaxAttempts, Attempt: m.Attempt}
			err := w.Handler(jobCtx, task)
			if err != nil {
				if err != context.Canceled && err != context.DeadlineExceeded {
					logger.Warn().Err(err).Str("status", "retry").Msg("job failed")
				}
				w.handleFailure(jobCtx, queueKey, processingKey, raw, m, retryBase, err)
				return
			}
			w.ack(jobCtx, processingKey, raw, m)
		}(raw, msg)
	}
}

func (w Worker) handleFailure(ctx context.Context, queueKey, processingKey, raw string, msg taskMessage, base time.Duration, cause error) {
	if raw != "" {
		_ = w.R.ZRem(ctx, processingKey, raw)
	}
	if cause != nil {
		msg.LastError = cause.Error()
	}
	if msg.MaxAttempts > 0 && msg.Attempt >= msg.MaxAttempts {
		w.pushToDLQ(ctx, raw, msg)
		if QueueProcessedTotal != nil {
			label := queueLabel(msg.Kind)
			QueueProcessedTotal.WithLabelValues(label, "dlq").Inc()
		}
		if msg.Key != "" {
			_ = w.R.Del(ctx, w.dedupKey(msg.Kind, msg.Key)).Err()
		}
		w.updateDLQSize(ctx, msg.Kind)
		return
	}
	delay := resilience.Backoff(base, msg.Attempt, w.RetryJitter)
	msg.AvailableAt = time.Now().Add(delay).UnixNano()
	rawBytes, err := json.Marshal(msg)
	if err != nil {
		return
	}
	_ = w.R.ZAdd(ctx, queueKey, redis.Z{Score: float64(msg.AvailableAt), Member: string(rawBytes)}).Err()
	if QueueProcessedTotal != nil {
		label := queueLabel(msg.Kind)
		QueueProcessedTotal.WithLabelValues(label, "retry").Inc()
	}
	w.updateDepth(ctx, queueKey, msg.Kind)
}

func (w Worker) ack(ctx context.Context, processingKey, raw string, msg taskMessage) {
	if raw != "" {
		_ = w.R.ZRem(ctx, processingKey, raw)
	}
	if msg.Key != "" {
		_ = w.R.Del(ctx, w.dedupKey(msg.Kind, msg.Key)).Err()
	}
	if QueueProcessedTotal != nil {
		QueueProcessedTotal.WithLabelValues(queueLabel(msg.Kind), "success").Inc()
	}
	w.updateDepth(ctx, w.queueKey(msg.Kind), msg.Kind)
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

func queueLabel(kind string) string {
	trimmed := strings.TrimSpace(kind)
	if trimmed == "" {
		return "default"
	}
	if sanitized := sanitizeKind(trimmed); sanitized != "" {
		return sanitized
	}
	return trimmed
}

func (w Worker) updateDepth(ctx context.Context, queueKey, kind string) {
	if QueueDepth == nil || w.R == nil {
		return
	}
	depth, err := w.R.ZCard(ctx, queueKey).Result()
	if err != nil {
		return
	}
	QueueDepth.WithLabelValues(queueLabel(kind)).Set(float64(depth))
}

func (w Worker) updateDLQSize(ctx context.Context, kind string) {
	if QueueDLQSize == nil || w.Store == nil {
		return
	}
	label := queueLabel(kind)
	count, err := w.Store.CountQueueDlq(ctx, label)
	if err != nil {
		return
	}
	QueueDLQSize.WithLabelValues(label).Set(float64(count))
}

func (w Worker) pushToDLQ(ctx context.Context, raw string, msg taskMessage) {
	if w.Store == nil {
		return
	}
	entry := DLQEntry{
		Kind:           msg.Kind,
		IdempotencyKey: msg.Key,
		Payload:        []byte(raw),
		Attempts:       msg.Attempt,
	}
	if msg.LastError != "" {
		entry.LastError = &msg.LastError
	}
	if _, err := w.Store.InsertQueueDlq(ctx, entry); err != nil {
		if !errors.Is(err, ErrStoreUnavailable) {
			w.logger().Error().Err(err).Str("queue_kind", queueLabel(msg.Kind)).Msg("insert dlq entry failed")
		}
	}
}

func (w Worker) logger() *zerolog.Logger {
	if w.Logger == nil {
		return &nopLogger
	}
	return w.Logger
}

type taskMessage struct {
	Kind        string `json:"kind"`
	Key         string `json:"key,omitempty"`
	Payload     []byte `json:"payload"`
	Attempt     int    `json:"attempt"`
	MaxAttempts int    `json:"max_attempts"`
	AvailableAt int64  `json:"available_at"`
	LastError   string `json:"last_error,omitempty"`
}
