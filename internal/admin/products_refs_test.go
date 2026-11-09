package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/tenant"
)

// refsStore owns the brands and categories listed for its tenant only. Calls
// to methods it does not implement panic through the nil embedded interface.
type refsStore struct {
	dbgen.Querier
	tenant     pgtype.UUID
	brands     map[pgtype.UUID]bool
	categories map[pgtype.UUID]bool
	created    int
	updated    int
}

func (s *refsStore) AdminBrandBelongsToTenant(_ context.Context, arg dbgen.AdminBrandBelongsToTenantParams) (bool, error) {
	return arg.TenantID == s.tenant && s.brands[arg.ID], nil
}

func (s *refsStore) AdminCategoryBelongsToTenant(_ context.Context, arg dbgen.AdminCategoryBelongsToTenantParams) (bool, error) {
	return arg.TenantID == s.tenant && s.categories[arg.ID], nil
}

func (s *refsStore) AdminCreateProduct(_ context.Context, _ dbgen.AdminCreateProductParams) (pgtype.UUID, error) {
	s.created++
	return pgUUID(uuid.New()), nil
}

func (s *refsStore) AdminGetProduct(_ context.Context, arg dbgen.AdminGetProductParams) (dbgen.AdminGetProductRow, error) {
	return dbgen.AdminGetProductRow{ID: arg.ID, Slug: "existing"}, nil
}

func (s *refsStore) AdminUpdateProduct(_ context.Context, arg dbgen.AdminUpdateProductParams) (pgtype.UUID, error) {
	s.updated++
	return arg.ID, nil
}

func pgUUID(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }

func TestProductWritesRejectForeignBrandAndCategory(t *testing.T) {
	tenantA := uuid.New()
	ownBrand, ownCategory := uuid.New(), uuid.New()
	foreign := uuid.New() // exists only in another tenant

	newStore := func() *refsStore {
		return &refsStore{
			tenant:     pgUUID(tenantA),
			brands:     map[pgtype.UUID]bool{pgUUID(ownBrand): true},
			categories: map[pgtype.UUID]bool{pgUUID(ownCategory): true},
		}
	}

	cases := []struct {
		name       string
		method     string
		body       string
		wantStatus int
	}{
		{"create with own refs", http.MethodPost, `{"title":"T","price":1,"brandId":"` + ownBrand.String() + `","categoryId":"` + ownCategory.String() + `"}`, http.StatusCreated},
		{"create with foreign brand", http.MethodPost, `{"title":"T","price":1,"brandId":"` + foreign.String() + `"}`, http.StatusBadRequest},
		{"create with foreign category", http.MethodPost, `{"title":"T","price":1,"categoryId":"` + foreign.String() + `"}`, http.StatusBadRequest},
		{"update with own brand", http.MethodPatch, `{"brandId":"` + ownBrand.String() + `"}`, http.StatusNoContent},
		{"update with foreign brand", http.MethodPatch, `{"brandId":"` + foreign.String() + `"}`, http.StatusBadRequest},
		{"update with foreign category", http.MethodPatch, `{"categoryId":"` + foreign.String() + `"}`, http.StatusBadRequest},
		{"update clearing category", http.MethodPatch, `{"categoryId":null}`, http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore()
			h := &CatalogHandler{Q: store}
			r := chi.NewRouter()
			r.Post("/products", h.CreateProduct)
			r.Patch("/products/{id}", h.UpdateProduct)

			path := "/products"
			if tc.method == http.MethodPatch {
				path += "/" + uuid.NewString()
			}
			req := httptest.NewRequest(tc.method, path, strings.NewReader(tc.body))
			req = req.WithContext(tenant.WithTenant(req.Context(), tenantA.String()))
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus == http.StatusBadRequest {
				if store.created+store.updated != 0 {
					t.Fatalf("rejected request must not write, got %d creates, %d updates", store.created, store.updated)
				}
				if !strings.Contains(rec.Body.String(), "unknown brand or category") {
					t.Fatalf("unexpected body %s", rec.Body.String())
				}
			}
		})
	}
}
