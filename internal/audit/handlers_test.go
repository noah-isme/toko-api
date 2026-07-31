package audit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

type listStore struct {
	stubStore
	received      dbgen.ListAuditLogsFilteredParams
	receivedCount dbgen.CountAuditLogsFilteredParams
	rows          []dbgen.AuditLog
	total         int64
}

func (l *listStore) ListAuditLogsFiltered(_ context.Context, arg dbgen.ListAuditLogsFilteredParams) ([]dbgen.AuditLog, error) {
	l.received = arg
	if l.rows != nil {
		return l.rows, nil
	}
	return []dbgen.AuditLog{{Action: "TEST", Method: "GET"}}, nil
}

func (l *listStore) CountAuditLogsFiltered(_ context.Context, arg dbgen.CountAuditLogsFilteredParams) (int64, error) {
	l.receivedCount = arg
	return l.total, nil
}

type envelope struct {
	Data       []map[string]any `json:"data"`
	Pagination struct {
		Page       int `json:"page"`
		PerPage    int `json:"perPage"`
		TotalItems int `json:"totalItems"`
	} `json:"pagination"`
}

func decodeEnvelope(t *testing.T, body []byte) envelope {
	t.Helper()
	var out envelope
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func TestHandlerListPagination(t *testing.T) {
	store := &listStore{total: 130}
	h := Handler{Store: store}
	req := httptest.NewRequest(http.MethodGet, "/audit?limit=25&offset=10", nil)
	rr := httptest.NewRecorder()
	h.List(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if store.received.Limit != 25 || store.received.Offset != 10 {
		t.Fatalf("unexpected pagination params: %d/%d", store.received.Limit, store.received.Offset)
	}
	got := decodeEnvelope(t, rr.Body.Bytes())
	if len(got.Data) != 1 {
		t.Fatalf("expected one log entry, got %d", len(got.Data))
	}
	if got.Pagination.PerPage != 25 || got.Pagination.TotalItems != 130 {
		t.Fatalf("unexpected pagination envelope: %+v", got.Pagination)
	}
}

// A `page` without an explicit `offset` must be translated into an offset.
func TestHandlerListPageDerivesOffset(t *testing.T) {
	store := &listStore{}
	h := Handler{Store: store}
	rr := httptest.NewRecorder()
	h.List(rr, httptest.NewRequest(http.MethodGet, "/audit?page=3&limit=20", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if store.received.Offset != 40 || store.received.Limit != 20 {
		t.Fatalf("expected offset 40 limit 20, got %d/%d", store.received.Offset, store.received.Limit)
	}
}

func TestHandlerListFilters(t *testing.T) {
	userID := uuid.NewString()
	store := &listStore{}
	h := Handler{Store: store}
	rr := httptest.NewRecorder()
	url := "/audit?userId=" + userID + "&action=cancel&resourceType=admin&startDate=2024-01-02&endDate=2024-02-03T10:30"
	h.List(rr, httptest.NewRequest(http.MethodGet, url, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	got := store.received
	if !got.ActorUserID.Valid || uuid.UUID(got.ActorUserID.Bytes).String() != userID {
		t.Fatalf("actor filter not forwarded: %+v", got.ActorUserID)
	}
	if got.Action.String != "cancel" || got.ResourceType.String != "admin" {
		t.Fatalf("text filters not forwarded: %+v %+v", got.Action, got.ResourceType)
	}
	if !got.StartDate.Valid || !got.EndDate.Valid {
		t.Fatalf("date filters not forwarded: %+v %+v", got.StartDate, got.EndDate)
	}
	// The count query must see the same filters, otherwise totals disagree with rows.
	if store.receivedCount.Action != got.Action || store.receivedCount.ActorUserID != got.ActorUserID {
		t.Fatalf("count filters diverge from list filters")
	}
}

func TestHandlerListRejectsBadFilters(t *testing.T) {
	cases := map[string]string{
		"bad uuid":       "/audit?userId=not-a-uuid",
		"bad start date": "/audit?startDate=yesterday",
		"bad end date":   "/audit?endDate=13/13/2024",
	}
	for name, url := range cases {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			Handler{Store: &listStore{}}.List(rr, httptest.NewRequest(http.MethodGet, url, nil))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d", rr.Code)
			}
		})
	}
}

// Rows must serialise as camelCase with metadata as an object, not base64.
func TestHandlerListSerialisesRow(t *testing.T) {
	id := uuid.New()
	actor := uuid.New()
	store := &listStore{rows: []dbgen.AuditLog{{
		ID:           pgtype.UUID{Bytes: id, Valid: true},
		ActorKind:    dbgen.ActorKindUser,
		ActorUserID:  pgtype.UUID{Bytes: actor, Valid: true},
		Action:       "POST /api/v1/admin/vouchers",
		ResourceType: "admin",
		ResourceID:   pgtype.Text{String: "v-1", Valid: true},
		Method:       http.MethodPost,
		Path:         "/api/v1/admin/vouchers",
		Status:       201,
		Ip:           pgtype.Text{String: "10.0.0.1", Valid: true},
		Metadata:     []byte(`{"query":"status=active"}`),
	}}}
	rr := httptest.NewRecorder()
	Handler{Store: store}.List(rr, httptest.NewRequest(http.MethodGet, "/audit", nil))
	got := decodeEnvelope(t, rr.Body.Bytes())
	if len(got.Data) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got.Data))
	}
	row := got.Data[0]
	if row["id"] != id.String() || row["userId"] != actor.String() {
		t.Fatalf("ids not serialised: %+v", row)
	}
	if row["actorKind"] != "user" || row["resourceType"] != "admin" || row["ipAddress"] != "10.0.0.1" {
		t.Fatalf("camelCase fields missing: %+v", row)
	}
	meta, ok := row["metadata"].(map[string]any)
	if !ok || meta["query"] != "status=active" {
		t.Fatalf("metadata not an object: %#v", row["metadata"])
	}
}

func TestHandlerListWithoutStore(t *testing.T) {
	rr := httptest.NewRecorder()
	Handler{}.List(rr, httptest.NewRequest(http.MethodGet, "/audit", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}
