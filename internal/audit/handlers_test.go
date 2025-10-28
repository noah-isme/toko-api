package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type listStore struct {
	stubStore
	receivedLimit  int32
	receivedOffset int32
}

func (l *listStore) ListAuditLogs(_ context.Context, arg dbgen.ListAuditLogsParams) ([]dbgen.AuditLog, error) {
	l.receivedLimit = arg.Limit
	l.receivedOffset = arg.Offset
	return []dbgen.AuditLog{{Action: "TEST", Method: "GET"}}, nil
}

func TestHandlerList(t *testing.T) {
	store := &listStore{}
	h := Handler{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/audit?limit=25&offset=10", nil)
	rr := httptest.NewRecorder()
	h.List(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if store.receivedLimit != 25 || store.receivedOffset != 10 {
		t.Fatalf("unexpected pagination params: %d/%d", store.receivedLimit, store.receivedOffset)
	}
	var payload []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected one log entry, got %d", len(payload))
	}
}
