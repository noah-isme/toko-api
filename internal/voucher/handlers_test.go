package voucher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// fakeVoucherStore embeds the nil interface and overrides only what the admin
// voucher handlers call; anything else panics, which flags unexpected queries.
type fakeVoucherStore struct {
	dbgen.Querier
	created      []dbgen.CreateVoucherParams
	updated      []dbgen.UpdateVoucherParams
	existsCalls  []dbgen.AdminVoucherExistsForTenantParams
	existsResult bool
	updateErr    error
}

func (f *fakeVoucherStore) CreateVoucher(_ context.Context, arg dbgen.CreateVoucherParams) (dbgen.Voucher, error) {
	f.created = append(f.created, arg)
	return dbgen.Voucher{Code: arg.Code, TenantID: arg.TenantID}, nil
}

func (f *fakeVoucherStore) UpdateVoucher(_ context.Context, arg dbgen.UpdateVoucherParams) (dbgen.Voucher, error) {
	f.updated = append(f.updated, arg)
	if f.updateErr != nil {
		return dbgen.Voucher{}, f.updateErr
	}
	return dbgen.Voucher{Code: arg.Code, TenantID: arg.TenantID}, nil
}

func (f *fakeVoucherStore) AdminVoucherExistsForTenant(_ context.Context, arg dbgen.AdminVoucherExistsForTenantParams) (bool, error) {
	f.existsCalls = append(f.existsCalls, arg)
	return f.existsResult, nil
}

func serveVoucher(h *Handler, method, path, pattern string, handler http.HandlerFunc, tenantID, body string) *httptest.ResponseRecorder {
	r := chi.NewRouter()
	r.Method(method, pattern, handler)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if tenantID != "" {
		req = req.WithContext(tenant.WithTenant(req.Context(), tenantID))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestCreateSetsTenantFromContext(t *testing.T) {
	tenantID := uuid.New()
	store := &fakeVoucherStore{}
	h := &Handler{Q: store}
	rec := serveVoucher(h, http.MethodPost, "/admin/vouchers", "/admin/vouchers", h.Create, tenantID.String(),
		`{"code":"NEW10","kind":"fixed_amount","value":1000}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	if len(store.created) != 1 {
		t.Fatalf("CreateVoucher calls = %d, want 1", len(store.created))
	}
	want := pgtype.UUID{Bytes: tenantID, Valid: true}
	if store.created[0].TenantID != want {
		t.Fatalf("TenantID = %v, want %v", store.created[0].TenantID, want)
	}
}

func TestCreateWithoutTenant400(t *testing.T) {
	store := &fakeVoucherStore{}
	h := &Handler{Q: store}
	rec := serveVoucher(h, http.MethodPost, "/admin/vouchers", "/admin/vouchers", h.Create, "",
		`{"code":"NEW10","kind":"fixed_amount","value":1000}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if len(store.created) != 0 {
		t.Fatal("CreateVoucher must not be called without a tenant")
	}
}

func TestUpdatePassesTenant(t *testing.T) {
	tenantID := uuid.New()
	store := &fakeVoucherStore{}
	h := &Handler{Q: store}
	rec := serveVoucher(h, http.MethodPut, "/admin/vouchers/A-ONLY", "/admin/vouchers/{code}", h.Update, tenantID.String(),
		`{"code":"IGNORED","kind":"fixed_amount","value":2000}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if len(store.updated) != 1 || store.updated[0].TenantID != (pgtype.UUID{Bytes: tenantID, Valid: true}) || store.updated[0].Code != "A-ONLY" {
		t.Fatalf("unexpected update params: %+v", store.updated)
	}
}

func TestUpdateForeignVoucher404(t *testing.T) {
	store := &fakeVoucherStore{updateErr: pgx.ErrNoRows}
	h := &Handler{Q: store}
	rec := serveVoucher(h, http.MethodPut, "/admin/vouchers/B-ONLY", "/admin/vouchers/{code}", h.Update, uuid.NewString(),
		`{"code":"IGNORED","kind":"fixed_amount","value":2000}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestPreviewUnknownVoucherInTenant404(t *testing.T) {
	tenantID := uuid.New()
	store := &fakeVoucherStore{existsResult: false}
	// Svc only needs to be non-nil; the handler must return before using it.
	h := &Handler{Q: store, Svc: &Service{}}
	rec := serveVoucher(h, http.MethodPost, "/admin/vouchers/preview", "/admin/vouchers/preview", h.Preview, tenantID.String(),
		`{"code":"B-ONLY","cartTotal":10000,"items":[{"subtotal":10000}]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
	if len(store.existsCalls) != 1 || store.existsCalls[0].TenantID != (pgtype.UUID{Bytes: tenantID, Valid: true}) || store.existsCalls[0].Code != "B-ONLY" {
		t.Fatalf("unexpected exists calls: %+v", store.existsCalls)
	}
}
