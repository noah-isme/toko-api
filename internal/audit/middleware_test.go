package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type countingStore struct {
	stubStore
	byMethod map[string]int
}

func (s *countingStore) InsertAuditLog(ctx context.Context, arg dbgen.InsertAuditLogParams) (dbgen.InsertAuditLogRow, error) {
	if s.byMethod == nil {
		s.byMethod = map[string]int{}
	}
	s.byMethod[arg.Method]++
	return s.stubStore.InsertAuditLog(ctx, arg)
}

func TestRecordSamplingSkipsOnlySafeMethods(t *testing.T) {
	store := &countingStore{}
	svc := Service{Store: store, Enabled: true, SamplingRate: 0.000001}
	for i := 0; i < 50; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/vouchers", nil)
		if err := svc.Record(req.Context(), Actor{Kind: ActorKindSystem}, "", "admin", "", req, http.StatusCreated, nil); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req := httptest.NewRequest(m, "/api/v1/admin/vouchers/X", nil)
		if err := svc.Record(req.Context(), Actor{Kind: ActorKindSystem}, "", "admin", "", req, http.StatusOK, nil); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	for i := 0; i < 200; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/vouchers", nil)
		if err := svc.Record(req.Context(), Actor{Kind: ActorKindSystem}, "", "admin", "", req, http.StatusOK, nil); err != nil {
			t.Fatalf("record: %v", err)
		}
	}
	if store.byMethod[http.MethodPost] != 50 {
		t.Fatalf("POST recorded %d times, want 50", store.byMethod[http.MethodPost])
	}
	for _, m := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		if store.byMethod[m] != 1 {
			t.Fatalf("%s recorded %d times, want 1", m, store.byMethod[m])
		}
	}
	if store.byMethod[http.MethodGet] >= 200 {
		t.Fatalf("GET recorded %d times, sampling should drop some", store.byMethod[http.MethodGet])
	}
}

func TestMiddlewareMergesAnnotations(t *testing.T) {
	store := &stubStore{}
	rec := HTTPRecorder{Service: &Service{Store: store, Enabled: true, SamplingRate: 1}}
	r := chi.NewRouter()
	r.Use(rec.Middleware(HTTPConfig{
		ResourceType: "admin",
		MetadataFunc: func(*http.Request, int) map[string]any {
			return map[string]any{"base": "yes", "newRole": "overridden"}
		},
	}))
	r.Patch("/staff/{id}", func(w http.ResponseWriter, req *http.Request) {
		Annotate(req.Context(), "targetUserId", "u1")
		Annotate(req.Context(), "newRole", "manager")
		w.WriteHeader(http.StatusConflict)
	})
	req := httptest.NewRequest(http.MethodPatch, "/staff/u1", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	if !store.called {
		t.Fatal("audit entry not recorded")
	}
	if store.lastInsert.Status != http.StatusConflict {
		t.Fatalf("status = %d, want 409", store.lastInsert.Status)
	}
	var meta map[string]any
	if err := json.Unmarshal(store.lastInsert.Metadata, &meta); err != nil {
		t.Fatalf("metadata: %v (%s)", err, store.lastInsert.Metadata)
	}
	if meta["targetUserId"] != "u1" || meta["newRole"] != "manager" || meta["base"] != "yes" {
		t.Fatalf("unexpected metadata %v", meta)
	}
}

func TestAnnotateOutsideMiddlewareIsNoop(t *testing.T) {
	Annotate(context.Background(), "k", "v")
	//nolint:staticcheck // a nil context must not panic either
	Annotate(nil, "k", "v")
}
