package common

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	redis "github.com/redis/go-redis/v9"
)

func newIdem(t *testing.T) (Idem, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	return Idem{R: client, TTL: 10 * time.Minute}, mr
}

func idemCall(h http.Handler, key string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func countingHandler(status *int, calls *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*calls++
		w.WriteHeader(*status)
	})
}

func TestIdemMiddlewarePassesThroughWithoutHeader(t *testing.T) {
	idem, _ := newIdem(t)
	status, calls := http.StatusOK, 0
	h := idem.Middleware(countingHandler(&status, &calls))
	for i := 0; i < 2; i++ {
		if rec := idemCall(h, ""); rec.Code != http.StatusOK {
			t.Fatalf("call %d: %d", i, rec.Code)
		}
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestIdemMiddlewareNilRedisPassesThrough(t *testing.T) {
	status, calls := http.StatusOK, 0
	h := Idem{}.Middleware(countingHandler(&status, &calls))
	idemCall(h, "k")
	idemCall(h, "k")
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestIdemMiddlewareRejectsReplay(t *testing.T) {
	idem, _ := newIdem(t)
	status, calls := http.StatusOK, 0
	h := idem.Middleware(countingHandler(&status, &calls))
	if rec := idemCall(h, "k1"); rec.Code != http.StatusOK {
		t.Fatalf("first: %d", rec.Code)
	}
	rec := idemCall(h, "k1")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "IDEMPOTENT_REPLAY") {
		t.Fatalf("replay: %d %s", rec.Code, rec.Body.String())
	}
	if rec := idemCall(h, "k2"); rec.Code != http.StatusOK {
		t.Fatalf("other key: %d", rec.Code)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestIdemMiddlewareReleasesKeyOn5xx(t *testing.T) {
	idem, mr := newIdem(t)
	status, calls := http.StatusBadGateway, 0
	h := idem.Middleware(countingHandler(&status, &calls))
	if rec := idemCall(h, "k"); rec.Code != http.StatusBadGateway {
		t.Fatalf("first: %d", rec.Code)
	}
	if mr.Exists(hashKey("k")) {
		t.Fatal("key must be released after a 5xx")
	}
	status = http.StatusOK
	if rec := idemCall(h, "k"); rec.Code != http.StatusOK {
		t.Fatalf("retry with same key: %d", rec.Code)
	}
	if rec := idemCall(h, "k"); rec.Code != http.StatusConflict {
		t.Fatalf("after success the key is locked: %d", rec.Code)
	}
	if calls != 2 {
		t.Fatalf("calls = %d", calls)
	}
}

func TestIdemMiddlewareKeepsKeyOn4xx(t *testing.T) {
	idem, mr := newIdem(t)
	status, calls := http.StatusConflict, 0
	h := idem.Middleware(countingHandler(&status, &calls))
	idemCall(h, "k")
	if !mr.Exists(hashKey("k")) {
		t.Fatal("key must be kept after a 4xx")
	}
	status = http.StatusOK
	if rec := idemCall(h, "k"); rec.Code != http.StatusConflict || calls != 1 {
		t.Fatalf("replay after 4xx: %d calls=%d", rec.Code, calls)
	}
}

func TestIdemMiddlewareKeepsKeyOnPanic(t *testing.T) {
	idem, mr := newIdem(t)
	h := idem.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }))
	func() {
		defer func() { _ = recover() }()
		idemCall(h, "k")
	}()
	if !mr.Exists(hashKey("k")) {
		t.Fatal("key must be kept after a panic")
	}
	if ttl := mr.TTL(hashKey("k")); ttl <= 0 {
		t.Fatalf("key must have a TTL, got %v", ttl)
	}
}

func TestIdemMiddlewareImplicitOKOnWrite(t *testing.T) {
	idem, mr := newIdem(t)
	h := idem.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	idemCall(h, "k")
	if !mr.Exists(hashKey("k")) {
		t.Fatal("implicit 200 must keep the key")
	}
	if http.NewResponseController(&idemStatusRecorder{ResponseWriter: httptest.NewRecorder()}).Flush() != nil {
		t.Fatal("recorder must unwrap to a flusher")
	}
}
