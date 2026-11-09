package analytics_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/noah-isme/backend-toko/internal/analytics"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

func doRequest(h http.HandlerFunc, target, tenantID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if tenantID != "" {
		req = req.WithContext(tenant.With(req.Context(), tenantID))
	}
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestSalesHandlerRequiresTenant(t *testing.T) {
	h := &analytics.Handler{Svc: &analytics.Service{Q: &stubQueries{}}}
	for name, fn := range map[string]http.HandlerFunc{"sales": h.Sales, "top": h.TopProducts, "overview": h.Overview} {
		rec := doRequest(fn, "/x", "")
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "TENANT_REQUIRED" {
			t.Fatalf("%s: expected 400 TENANT_REQUIRED, got %d %s", name, rec.Code, rec.Body.String())
		}
		rec = doRequest(fn, "/x", "not-a-uuid")
		if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "TENANT_REQUIRED" {
			t.Fatalf("%s: expected 400 TENANT_REQUIRED for bad tenant id, got %d", name, rec.Code)
		}
	}
	// Tenant id present but no tenants row.
	h = &analytics.Handler{Svc: &analytics.Service{Q: &stubQueries{tzErr: pgx.ErrNoRows}}}
	rec := doRequest(h.Sales, "/x", uuid.NewString())
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "TENANT_REQUIRED" {
		t.Fatalf("expected 400 TENANT_REQUIRED for unknown tenant, got %d", rec.Code)
	}
}

func TestSalesHandlerPassesTenant(t *testing.T) {
	q := &stubQueries{}
	h := &analytics.Handler{Svc: &analytics.Service{Q: q}}
	id := uuid.New()
	rec := doRequest(h.Sales, "/x?from=2026-10-01T00:00:00Z&to=2026-10-02T00:00:00Z", id.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	if len(q.salesArgs) != 1 || q.salesArgs[0].TenantID.Bytes != id || q.salesArgs[0].Tz != "UTC" {
		t.Fatalf("tenant not passed through: %+v", q.salesArgs)
	}
}

func TestSalesHandlerDefaultRangeIsLocalMidnight(t *testing.T) {
	q := &stubQueries{timezone: "Asia/Jakarta"}
	// 2026-10-10 20:00 UTC is already 2026-10-11 03:00 in Jakarta.
	now := time.Date(2026, 10, 10, 20, 0, 0, 0, time.UTC)
	h := &analytics.Handler{Svc: &analytics.Service{Q: q, DefaultRange: 30, Now: func() time.Time { return now }}}
	rec := doRequest(h.Sales, "/x?days=7", uuid.NewString())
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	arg := q.salesArgs[0]
	wantEnd := time.Date(2026, 10, 11, 17, 0, 0, 0, time.UTC) // 2026-10-12 00:00 WIB
	if !arg.EndDate.Time.Equal(wantEnd) {
		t.Fatalf("end = %s, want %s", arg.EndDate.Time.UTC(), wantEnd)
	}
	if !arg.StartDate.Time.Equal(wantEnd.AddDate(0, 0, -7)) {
		t.Fatalf("start = %s, want %s", arg.StartDate.Time.UTC(), wantEnd.AddDate(0, 0, -7))
	}
	if arg.Tz != "Asia/Jakarta" {
		t.Fatalf("tz = %q", arg.Tz)
	}
}

func TestTopProductsHandlerRange(t *testing.T) {
	q := &stubQueries{}
	h := &analytics.Handler{Svc: &analytics.Service{Q: q}}
	id := uuid.NewString()
	rec := doRequest(h.TopProducts, "/x?from=2026-10-02T00:00:00Z&to=2026-10-01T00:00:00Z", id)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for from >= to, got %d", rec.Code)
	}
	rec = doRequest(h.TopProducts, "/x?from=garbage", id)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad from, got %d", rec.Code)
	}
	rec = doRequest(h.TopProducts, "/x?from=2026-10-01T00:00:00Z&limit=5&offset=10", id)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", rec.Code, rec.Body.String())
	}
	arg := q.topArgs[0]
	if !arg.StartAt.Valid || arg.EndAt.Valid || arg.LimitCount != 5 || arg.OffsetRows != 10 {
		t.Fatalf("unexpected args: %+v", arg)
	}
	rec = doRequest(h.TopProducts, "/x", id)
	if rec.Code != http.StatusOK || q.topArgs[1].StartAt.Valid || q.topArgs[1].EndAt.Valid {
		t.Fatalf("expected unbounded all-time query, got %d %+v", rec.Code, q.topArgs[1])
	}
	var body struct {
		Data []any `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body.Data == nil {
		t.Fatalf("expected data to be an empty array, got %s", rec.Body.String())
	}
}
