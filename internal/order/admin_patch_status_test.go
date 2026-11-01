package order

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

// fakeAdminOrderStore stores one order owned by one tenant and answers like a
// tenant-filtered query: any other tenant sees pgx.ErrNoRows.
type fakeAdminOrderStore struct {
	orderID  pgtype.UUID
	tenantID pgtype.UUID
	status   dbgen.OrderStatus
	updates  int
}

func (f *fakeAdminOrderStore) AdminGetOrderStatus(_ context.Context, arg dbgen.AdminGetOrderStatusParams) (dbgen.OrderStatus, error) {
	if arg.ID != f.orderID || arg.TenantID != f.tenantID {
		return "", pgx.ErrNoRows
	}
	return f.status, nil
}

func (f *fakeAdminOrderStore) AdminUpdateOrderStatusIfAllowed(_ context.Context, arg dbgen.AdminUpdateOrderStatusIfAllowedParams) (pgtype.UUID, error) {
	if arg.ID != f.orderID || arg.TenantID != f.tenantID {
		return pgtype.UUID{}, pgx.ErrNoRows
	}
	f.updates++
	f.status = arg.Status
	return f.orderID, nil
}

func newPgUUID() (pgtype.UUID, string) {
	id := uuid.New()
	return pgtype.UUID{Bytes: id, Valid: true}, id.String()
}

func patchStatus(t *testing.T, h *AdminHandler, tenantID, orderID, body string) int {
	t.Helper()
	r := chi.NewRouter()
	r.Patch("/admin/orders/{id}/status", h.PatchStatus)
	req := httptest.NewRequest(http.MethodPatch, "/admin/orders/"+orderID+"/status", strings.NewReader(body))
	if tenantID != "" {
		req = req.WithContext(tenant.WithTenant(req.Context(), tenantID))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

func TestPatchStatusIsTenantScoped(t *testing.T) {
	orderID, orderStr := newPgUUID()
	ownerTenant, ownerTenantStr := newPgUUID()
	_, otherTenantStr := newPgUUID()

	t.Run("other tenant gets 404 and the order is untouched", func(t *testing.T) {
		store := &fakeAdminOrderStore{orderID: orderID, tenantID: ownerTenant, status: dbgen.OrderStatusPENDINGPAYMENT}
		code := patchStatus(t, &AdminHandler{Q: store}, otherTenantStr, orderStr, `{"status":"CANCELLED"}`)
		if code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", code)
		}
		if store.updates != 0 || store.status != dbgen.OrderStatusPENDINGPAYMENT {
			t.Fatalf("order changed: updates=%d status=%s", store.updates, store.status)
		}
	})

	t.Run("owning tenant can transition", func(t *testing.T) {
		store := &fakeAdminOrderStore{orderID: orderID, tenantID: ownerTenant, status: dbgen.OrderStatusPENDINGPAYMENT}
		code := patchStatus(t, &AdminHandler{Q: store}, ownerTenantStr, orderStr, `{"status":"CANCELLED"}`)
		if code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204", code)
		}
		if store.status != dbgen.OrderStatusCANCELLED {
			t.Fatalf("status = %s, want CANCELLED", store.status)
		}
	})

	t.Run("missing tenant is rejected before any read", func(t *testing.T) {
		store := &fakeAdminOrderStore{orderID: orderID, tenantID: ownerTenant, status: dbgen.OrderStatusPENDINGPAYMENT}
		code := patchStatus(t, &AdminHandler{Q: store}, "", orderStr, `{"status":"CANCELLED"}`)
		if code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", code)
		}
	})
}
