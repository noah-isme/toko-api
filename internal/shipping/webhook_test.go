package shipping_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/shipping"
)

type fakeReplayStore struct {
	results []bool
	err     error
}

func (f *fakeReplayStore) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	if len(f.results) == 0 {
		return redis.NewBoolResult(true, f.err)
	}
	res := f.results[0]
	f.results = f.results[1:]
	return redis.NewBoolResult(res, f.err)
}

func TestWebhookReplayProtection(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	orderID := uuid.New()
	userID := uuid.New()

	queries := newMockQueries()
	queries.addOrder(dbgen.Order{ID: toPGUUID(orderID), UserID: toPGUUID(userID), Status: dbgen.OrderStatusPAID}, "buyer@example.com")

	svc := &shipping.Service{
		Q:                      queries,
		Mail:                   &recordingMailer{},
		NotifyOnShipped:        true,
		NotifyOnOutForDelivery: true,
		NotifyOnDelivered:      true,
	}

	_, err := svc.Create(ctx, toPGUUID(orderID), "jne", "TRACK123")
	require.NoError(t, err)

	replay := &fakeReplayStore{results: []bool{true, false}}
	wh := shipping.Webhook{Svc: svc, Replay: replay, ReplayTTL: time.Minute}

	payload := map[string]any{"orderId": orderID.String(), "externalStatus": "shipped"}
	body, err := json.Marshal(payload)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/shipping/mock", bytes.NewReader(body))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("courier", "mock")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rr := httptest.NewRecorder()
	wh.Handle(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
	require.Len(t, queries.events, 1)

	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/shipping/mock", bytes.NewReader(body))
	rctx2 := chi.NewRouteContext()
	rctx2.URLParams.Add("courier", "mock")
	req2 = req2.WithContext(context.WithValue(req2.Context(), chi.RouteCtxKey, rctx2))

	rr2 := httptest.NewRecorder()
	wh.Handle(rr2, req2)
	require.Equal(t, http.StatusConflict, rr2.Code)
	require.Len(t, queries.events, 1)
}
