package queue_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/queue"
)

func TestDLQReplay(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)

	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	store := newMemoryStore()
	handler := queue.AdminHandler{
		Store:             store,
		Queue:             queue.Enqueuer{R: client, Prefix: "adm", DedupTTL: time.Minute, MaxAttempts: 5},
		PageSize:          10,
		VisibilityTimeout: 60 * time.Second,
	}

	raw, err := json.Marshal(struct {
		Kind        string `json:"kind"`
		Key         string `json:"key"`
		Payload     []byte `json:"payload"`
		Attempt     int    `json:"attempt"`
		MaxAttempts int    `json:"max_attempts"`
		AvailableAt int64  `json:"available_at"`
	}{
		Kind:        "webhook",
		Key:         "dlq1",
		Payload:     []byte("payload"),
		Attempt:     2,
		MaxAttempts: 3,
		AvailableAt: time.Now().UnixNano(),
	})
	require.NoError(t, err)

	entry := queue.DLQEntry{
		Kind:           "webhook",
		IdempotencyKey: "dlq1",
		Payload:        raw,
		Attempts:       2,
		CreatedAt:      time.Now(),
	}
	id, err := store.InsertQueueDlq(context.Background(), entry)
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"ids":["` + id.String() + `"]}`)
	req := httptest.NewRequest(http.MethodPost, "/admin/queue/dlq/replay", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handler.ReplayDLQ(rr, req)

	res := rr.Result()
	require.Equal(t, http.StatusOK, res.StatusCode)
	defer func() { _ = res.Body.Close() }()

	var resp struct {
		Replayed []string          `json:"replayed"`
		Failed   map[string]string `json:"failed"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&resp))
	require.Contains(t, resp.Replayed, id.String())
	require.Empty(t, resp.Failed)

	depth, err := client.ZCard(context.Background(), "adm:queue:webhook").Result()
	require.NoError(t, err)
	require.Equal(t, int64(1), depth)

	_, err = store.GetQueueDlq(context.Background(), id)
	require.ErrorIs(t, err, sql.ErrNoRows)
}
