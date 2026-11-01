package invoice

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

type stubBuilder struct {
	res      Result
	err      error
	gotUser  pgtype.UUID
	gotOrder pgtype.UUID
	called   bool
}

func (s *stubBuilder) Build(_ context.Context, _, orderID, userID pgtype.UUID) (Result, error) {
	s.called = true
	s.gotOrder, s.gotUser = orderID, userID
	return s.res, s.err
}

func invoiceRouter(b Builder) http.Handler {
	h := &Handler{Svc: b}
	r := chi.NewRouter()
	r.Get("/orders/{orderId}/invoice", h.UserInvoice)
	r.Get("/admin/orders/{id}/invoice", h.AdminInvoice)
	return r
}

func withIdentity(req *http.Request, user bool) *http.Request {
	ctx := tenant.WithTenant(req.Context(), uuid.NewString())
	if user {
		ctx = common.WithUserID(ctx, uuid.NewString())
	}
	return req.WithContext(ctx)
}

func TestUserInvoiceRequiresAuth(t *testing.T) {
	b := &stubBuilder{}
	rec := httptest.NewRecorder()
	invoiceRouter(b).ServeHTTP(rec, withIdentity(httptest.NewRequest(http.MethodGet, "/orders/"+uuid.NewString()+"/invoice", nil), false))
	if rec.Code != http.StatusUnauthorized || b.called {
		t.Fatalf("status = %d called=%v", rec.Code, b.called)
	}
}

func TestInvoiceErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		path string
		err  error
		want int
		code string
	}{
		{"bad user order id", "/orders/nope/invoice", nil, 400, "BAD_REQUEST"},
		{"bad admin order id", "/admin/orders/nope/invoice", nil, 400, "BAD_REQUEST"},
		{"not found", "/orders/" + uuid.NewString() + "/invoice", ErrNotFound, 404, "NOT_FOUND"},
		{"admin not found", "/admin/orders/" + uuid.NewString() + "/invoice", ErrNotFound, 404, "NOT_FOUND"},
		{"not eligible", "/orders/" + uuid.NewString() + "/invoice", ErrNotEligible, 409, "INVOICE_NOT_AVAILABLE"},
		{"internal", "/orders/" + uuid.NewString() + "/invoice", errors.New("db"), 500, "INTERNAL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			invoiceRouter(&stubBuilder{err: tc.err}).ServeHTTP(rec, withIdentity(httptest.NewRequest(http.MethodGet, tc.path, nil), true))
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("error content type = %q", ct)
			}
		})
	}
}

func TestInvoiceSuccessHeaders(t *testing.T) {
	body := []byte("%PDF-1.3 fake")
	for _, path := range []string{"/orders/" + uuid.NewString() + "/invoice", "/admin/orders/" + uuid.NewString() + "/invoice"} {
		b := &stubBuilder{res: Result{Number: "INV-2026-000001", Filename: "INV-2026-000001.pdf", PDF: body}}
		rec := httptest.NewRecorder()
		invoiceRouter(b).ServeHTTP(rec, withIdentity(httptest.NewRequest(http.MethodGet, path, nil), true))
		if rec.Code != 200 {
			t.Fatalf("%s status = %d", path, rec.Code)
		}
		h := rec.Header()
		if h.Get("Content-Type") != "application/pdf" ||
			h.Get("Content-Disposition") != `attachment; filename="INV-2026-000001.pdf"` ||
			h.Get("Cache-Control") != "private, no-store" ||
			h.Get("Content-Length") != "13" {
			t.Fatalf("%s headers = %v", path, h)
		}
		if rec.Body.String() != string(body) {
			t.Fatal("body mismatch")
		}
		isAdmin := path[:7] == "/admin/"
		if isAdmin == b.gotUser.Valid {
			t.Fatalf("%s: user filter valid=%v, want %v", path, b.gotUser.Valid, !isAdmin)
		}
	}
}
