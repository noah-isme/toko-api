package push

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/go-chi/chi/v5"
	redis "github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/ratelimit"
)

// Mount must put the limiter on send-test (and only there): the route fans out
// one outbound request per stored subscription.
func TestMountRateLimitsSendTestOnly(t *testing.T) {
	mr, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	user := newID()
	store := &stubStore{subs: []dbgen.PushSubscription{subFor(user, fcmEndpoint)}}
	h := &Handler{Svc: enabledService(store, &fakeSender{})}
	limit := ratelimit.Handler{
		Limiter: ratelimit.Limiter{Client: client, Prefix: "rl"},
		Config: ratelimit.Config{
			Key: func(r *http.Request) string {
				id, _ := common.UserID(r.Context())
				return "push-test:user:" + id
			},
			Window: time.Minute,
			Max:    1,
		},
	}.Middleware
	r := chi.NewRouter()
	r.Route("/push", func(p chi.Router) { h.Mount(p, limit) })

	do := func(method, path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, authed(httptest.NewRequest(method, path, nil), user))
		return rec
	}
	require.Equal(t, http.StatusOK, do(http.MethodPost, "/push/send-test").Code)
	limited := do(http.MethodPost, "/push/send-test")
	require.Equal(t, http.StatusTooManyRequests, limited.Code)
	require.NotEmpty(t, limited.Header().Get("Retry-After"))
	for i := 0; i < 3; i++ {
		require.Equal(t, http.StatusOK, do(http.MethodGet, "/push/vapid-key").Code, "other push routes are not limited")
	}
}

func TestMountRequiresLimiter(t *testing.T) {
	require.Panics(t, func() { (&Handler{}).Mount(chi.NewRouter(), nil) })
}
