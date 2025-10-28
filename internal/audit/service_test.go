package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/obs"
)

type stubStore struct {
	lastInsert dbgen.InsertAuditLogParams
	called     bool
}

func (s *stubStore) InsertAuditLog(ctx context.Context, arg dbgen.InsertAuditLogParams) (dbgen.InsertAuditLogRow, error) {
	s.called = true
	s.lastInsert = arg
	return dbgen.InsertAuditLogRow{}, nil
}

func (s *stubStore) ListAuditLogs(ctx context.Context, arg dbgen.ListAuditLogsParams) ([]dbgen.AuditLog, error) {
	return nil, nil
}

func TestServiceRecord(t *testing.T) {
	store := &stubStore{}
	svc := Service{Store: store, Enabled: true, SamplingRate: 1}
	userID := uuid.NewString()

	req := httptest.NewRequest(http.MethodPost, "https://api.test/api/v1/admin/vouchers?status=active", nil)
	req.Header.Set("User-Agent", "tester")
	req.Header.Set("X-Request-ID", "req-123")
	req.RemoteAddr = "10.0.0.2:54321"
	ctx := common.WithUserID(req.Context(), userID)
	ctx = obs.WithRoutePattern(ctx, "/api/v1/admin/vouchers")
	req = req.WithContext(ctx)

	if err := svc.Record(req.Context(), Actor{Kind: ActorKindUser, UserID: &userID}, "", "", "", req, http.StatusCreated, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if !store.called {
		t.Fatal("expected store to be called")
	}
	if store.lastInsert.ActorKind != string(ActorKindUser) {
		t.Fatalf("unexpected actor kind: %s", store.lastInsert.ActorKind)
	}
	if !store.lastInsert.ActorUserID.Valid {
		t.Fatal("expected user id to be stored")
	}
	actualUUID, err := uuid.FromBytes(store.lastInsert.ActorUserID.Bytes[:])
	if err != nil {
		t.Fatalf("decode uuid: %v", err)
	}
	if actualUUID.String() != userID {
		t.Fatalf("unexpected stored user id: %s", actualUUID.String())
	}
	if store.lastInsert.Action != "POST /api/v1/admin/vouchers" {
		t.Fatalf("unexpected action: %s", store.lastInsert.Action)
	}
	if store.lastInsert.ResourceType != "admin.vouchers" {
		t.Fatalf("unexpected resource type: %s", store.lastInsert.ResourceType)
	}
	if !store.lastInsert.Ip.Valid || store.lastInsert.Ip.String != "10.0.0.2" {
		t.Fatalf("expected ip capture, got %+v", store.lastInsert.Ip)
	}
	if !store.lastInsert.RequestID.Valid || store.lastInsert.RequestID.String != "req-123" {
		t.Fatalf("expected request id, got %+v", store.lastInsert.RequestID)
	}
	if len(store.lastInsert.Metadata) == 0 {
		t.Fatal("expected metadata to be set")
	}
	var meta map[string]string
	if err := json.Unmarshal(store.lastInsert.Metadata, &meta); err != nil {
		t.Fatalf("metadata json: %v", err)
	}
	if meta["query"] != "status=active" {
		t.Fatalf("unexpected metadata query: %s", meta["query"])
	}
}

func TestServiceRecordDisabled(t *testing.T) {
	store := &stubStore{}
	svc := Service{Store: store, Enabled: false}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if err := svc.Record(req.Context(), Actor{}, "", "", "", req, http.StatusOK, nil); err != nil {
		t.Fatalf("record: %v", err)
	}
	if store.called {
		t.Fatal("expected no insert when disabled")
	}
}
