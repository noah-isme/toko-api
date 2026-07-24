package notifications

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/noah-isme/backend-toko/internal/common"
	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

func authedRequest(method, target string) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	ctx := common.WithUserID(req.Context(), uuid.NewString())
	ctx = tenant.WithTenant(ctx, uuid.NewString())
	return req.WithContext(ctx)
}

func TestHandlerListReturnsPagination(t *testing.T) {
	store := &stubStore{
		listRows:   []dbgen.Notification{{ID: newUUID(), Type: "order_paid", Title: "Paid", Body: "ok", Data: []byte(`{"orderId":"x"}`), CreatedAt: validNow()}},
		countTotal: 1,
	}
	h := &Handler{Svc: &Service{Q: store}}

	rec := httptest.NewRecorder()
	h.List(rec, authedRequest(http.MethodGet, "https://api.test/api/v1/notifications"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var body struct {
		Data       []dto             `json:"data"`
		Pagination common.Pagination `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].Type != "order_paid" {
		t.Fatalf("unexpected data: %+v", body.Data)
	}
	if body.Data[0].Read {
		t.Fatal("notification with null read_at should be unread")
	}
	if body.Pagination.TotalItems != 1 {
		t.Fatalf("unexpected total: %d", body.Pagination.TotalItems)
	}
}

func TestHandlerListRequiresAuth(t *testing.T) {
	h := &Handler{Svc: &Service{Q: &stubStore{}}}
	rec := httptest.NewRecorder()
	// No user id in context.
	req := httptest.NewRequest(http.MethodGet, "https://api.test/api/v1/notifications", nil)
	h.List(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHandlerUnreadCount(t *testing.T) {
	store := &stubStore{countUnread: 3}
	h := &Handler{Svc: &Service{Q: store}}
	rec := httptest.NewRecorder()
	h.UnreadCount(rec, authedRequest(http.MethodGet, "https://api.test/api/v1/notifications/unread-count"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	var body struct {
		Unread int64 `json:"unread"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Unread != 3 {
		t.Fatalf("unexpected unread: %d", body.Unread)
	}
}

func TestHandlerMarkAllRead(t *testing.T) {
	store := &stubStore{}
	h := &Handler{Svc: &Service{Q: store}}
	rec := httptest.NewRecorder()
	h.MarkAllRead(rec, authedRequest(http.MethodPost, "https://api.test/api/v1/notifications/read-all"))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
	if !store.markedAll {
		t.Fatal("expected MarkAllNotificationsRead to be called")
	}
}
