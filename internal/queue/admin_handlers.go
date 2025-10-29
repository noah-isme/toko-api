package queue

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/noah-isme/backend-toko/internal/common"
)

// AdminHandler exposes queue management endpoints for DLQ operations and metrics.
type AdminHandler struct {
	Store             Store
	Queue             Enqueuer
	PageSize          int
	Logger            zerolog.Logger
	VisibilityTimeout time.Duration
}

// ListDLQ returns DLQ entries filtered by kind with pagination.
func (h *AdminHandler) ListDLQ(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "queue store unavailable", nil)
		return
	}
	ctx := r.Context()
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	limit, offset := parsePagination(r, h.pageSize())

	storeKind := kind
	if storeKind != "" {
		if sanitized := sanitizeKind(storeKind); sanitized != "" {
			storeKind = sanitized
		}
	}

	entries, err := h.Store.ListQueueDlq(ctx, storeKind, limit, offset)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	total, err := h.Store.CountQueueDlq(ctx, storeKind)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}

	items := make([]dlqItem, 0, len(entries))
	for _, entry := range entries {
		msg, err := decodeMessage(string(entry.Payload))
		if err != nil {
			continue
		}
		item := dlqItem{
			ID:             entry.ID,
			Kind:           entry.Kind,
			IdempotencyKey: entry.IdempotencyKey,
			Attempts:       int32(entry.Attempts),
			CreatedAt:      entry.CreatedAt,
			Message:        msg,
		}
		if entry.LastError != nil {
			item.LastError = entry.LastError
		}
		items = append(items, item)
	}

	resp := map[string]any{
		"data":  items,
		"total": total,
	}
	if storeKind != "" {
		resp["kind"] = storeKind
	}
	common.JSON(w, http.StatusOK, resp)
}

// ReplayDLQ re-enqueues DLQ entries either by ID list or batch by kind.
func (h *AdminHandler) ReplayDLQ(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Store == nil || h.Queue.R == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "queue dependencies unavailable", nil)
		return
	}
	var req replayRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid payload", nil)
		return
	}
	ids := uniqueStrings(req.IDs)
	kind := strings.TrimSpace(req.Kind)
	storeKind := kind
	if storeKind != "" {
		if sanitized := sanitizeKind(storeKind); sanitized != "" {
			storeKind = sanitized
		}
	}
	ctx := r.Context()
	replayed := make([]uuid.UUID, 0, len(ids))
	failed := make(map[string]string)

	if len(ids) == 0 && storeKind == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "ids or kind required", nil)
		return
	}

	if len(ids) > 0 {
		for _, raw := range ids {
			id, err := uuid.Parse(strings.TrimSpace(raw))
			if err != nil {
				failed[raw] = "invalid uuid"
				continue
			}
			entry, err := h.Store.GetQueueDlq(ctx, id)
			if err != nil {
				failed[raw] = err.Error()
				continue
			}
			if err := h.requeueEntry(ctx, entry); err != nil {
				failed[id.String()] = err.Error()
				continue
			}
			replayed = append(replayed, id)
		}
	} else {
		limit := req.Limit
		if limit <= 0 {
			limit = h.pageSize()
		}
		entries, err := h.Store.ListQueueDlq(ctx, storeKind, limit, 0)
		if err != nil {
			common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
			return
		}
		for _, entry := range entries {
			if err := h.requeueEntry(ctx, entry); err != nil {
				failed[entry.ID.String()] = err.Error()
				continue
			}
			replayed = append(replayed, entry.ID)
		}
	}

	resp := map[string]any{
		"replayed": replayed,
	}
	if len(failed) > 0 {
		resp["failed"] = failed
	}
	common.JSON(w, http.StatusOK, resp)
}

// Stats returns queue depth, processing and DLQ size for a given kind.
func (h *AdminHandler) Stats(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.Queue.R == nil || h.Store == nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", "queue dependencies unavailable", nil)
		return
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	if kind == "" {
		common.JSONError(w, http.StatusBadRequest, "BAD_REQUEST", "kind is required", nil)
		return
	}
	storeKind := kind
	if sanitized := sanitizeKind(storeKind); sanitized != "" {
		storeKind = sanitized
	}
	ctx := r.Context()
	queueKey := h.Queue.queueKey(storeKind)
	worker := Worker{R: h.Queue.R, Prefix: h.Queue.Prefix}
	processingKey := worker.processingKey(storeKind)

	ready, err := h.Queue.R.ZCard(ctx, queueKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	inflight, err := h.Queue.R.ZCard(ctx, processingKey).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}
	dlq, err := h.Store.CountQueueDlq(ctx, storeKind)
	if err != nil {
		common.JSONError(w, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return
	}

	var lagMillis int64
	oldest, err := h.Queue.R.ZRangeWithScores(ctx, queueKey, 0, 0).Result()
	if err == nil && len(oldest) > 0 {
		ts := time.Unix(0, int64(oldest[0].Score))
		if ts.Before(time.Now()) {
			lagMillis = time.Since(ts).Milliseconds()
		}
	}

	h.updateDepthMetric(ctx, storeKind)
	h.updateDLQMetric(ctx, storeKind)

	visibility := h.VisibilityTimeout
	if visibility <= 0 {
		visibility = 60 * time.Second
	}
	resp := map[string]any{
		"kind":               storeKind,
		"ready":              ready,
		"processing":         inflight,
		"dlq":                dlq,
		"oldest_lag_ms":      lagMillis,
		"visibility_timeout": visibility.Seconds(),
	}
	common.JSON(w, http.StatusOK, resp)
}

func (h *AdminHandler) requeueEntry(ctx context.Context, entry DLQEntry) error {
	msg, err := decodeMessage(string(entry.Payload))
	if err != nil {
		return err
	}
	attempt := msg.Attempt
	if attempt > 0 {
		attempt--
	}
	task := Task{
		Kind:           msg.Kind,
		Payload:        msg.Payload,
		IdempotencyKey: msg.Key,
		MaxAttempts:    msg.MaxAttempts,
		Attempt:        attempt,
	}
	if err := h.Queue.Enqueue(ctx, task); err != nil {
		return err
	}
	if err := h.Store.DeleteQueueDlq(ctx, entry.ID); err != nil {
		return err
	}
	h.updateDLQMetric(ctx, msg.Kind)
	h.updateDepthMetric(ctx, msg.Kind)
	return nil
}

func (h *AdminHandler) updateDLQMetric(ctx context.Context, kind string) {
	if QueueDLQSize == nil || h.Store == nil {
		return
	}
	count, err := h.Store.CountQueueDlq(ctx, queueLabel(kind))
	if err != nil {
		return
	}
	QueueDLQSize.WithLabelValues(queueLabel(kind)).Set(float64(count))
}

func (h *AdminHandler) updateDepthMetric(ctx context.Context, kind string) {
	if QueueDepth == nil || h.Queue.R == nil {
		return
	}
	queueKey := h.Queue.queueKey(kind)
	depth, err := h.Queue.R.ZCard(ctx, queueKey).Result()
	if err != nil {
		return
	}
	QueueDepth.WithLabelValues(queueLabel(kind)).Set(float64(depth))
}

func (h *AdminHandler) pageSize() int {
	if h.PageSize <= 0 {
		return 50
	}
	return h.PageSize
}

func parsePagination(r *http.Request, defaultLimit int) (limit, offset int) {
	limit = defaultLimit
	offset = 0
	if limit <= 0 {
		limit = 50
	}
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 200 {
			limit = parsed
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("offset")); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			offset = parsed
		}
	}
	return
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

type dlqItem struct {
	ID             uuid.UUID   `json:"id"`
	Kind           string      `json:"kind"`
	IdempotencyKey string      `json:"idempotencyKey"`
	Attempts       int32       `json:"attempts"`
	LastError      *string     `json:"lastError,omitempty"`
	CreatedAt      time.Time   `json:"createdAt"`
	Message        taskMessage `json:"message"`
}

type replayRequest struct {
	IDs   []string `json:"ids"`
	Kind  string   `json:"kind"`
	Limit int      `json:"limit"`
}
