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

// categoryRefsStore extends refsStore with the category writes so the parent
// ownership check can be exercised without a database.
type categoryRefsStore struct {
	refsStore
}

func (s *categoryRefsStore) AdminCreateCategory(_ context.Context, arg dbgen.AdminCreateCategoryParams) (dbgen.AdminCreateCategoryRow, error) {
	s.created++
	return dbgen.AdminCreateCategoryRow{ID: pgUUID(uuid.New()), Name: arg.Name, Slug: arg.Slug, ParentID: arg.ParentID}, nil
}

func (s *categoryRefsStore) AdminUpdateCategory(_ context.Context, arg dbgen.AdminUpdateCategoryParams) (dbgen.AdminUpdateCategoryRow, error) {
	s.updated++
	return dbgen.AdminUpdateCategoryRow{ID: arg.ID, Name: "n", Slug: "s", ParentID: arg.ParentID}, nil
}

func TestCategoryWritesRejectForeignParent(t *testing.T) {
	tenantA := uuid.New()
	ownCategory := uuid.New()
	foreign := uuid.New() // exists only in another tenant

	cases := []struct {
		name       string
		method     string
		body       string
		wantStatus int
	}{
		{"create with own parent", http.MethodPost, `{"name":"Shoes","parentId":"` + ownCategory.String() + `"}`, http.StatusCreated},
		{"create without parent", http.MethodPost, `{"name":"Shoes"}`, http.StatusCreated},
		{"create with foreign parent", http.MethodPost, `{"name":"Shoes","parentId":"` + foreign.String() + `"}`, http.StatusBadRequest},
		{"update with own parent", http.MethodPatch, `{"parentId":"` + ownCategory.String() + `"}`, http.StatusOK},
		{"update with foreign parent", http.MethodPatch, `{"parentId":"` + foreign.String() + `"}`, http.StatusBadRequest},
		{"update clearing parent", http.MethodPatch, `{"parentId":null}`, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &categoryRefsStore{refsStore{
				tenant:     pgUUID(tenantA),
				categories: map[pgtype.UUID]bool{pgUUID(ownCategory): true},
			}}
			h := &CatalogHandler{Q: store}
			r := chi.NewRouter()
			r.Post("/categories", h.CreateCategory)
			r.Patch("/categories/{id}", h.UpdateCategory)

			path := "/categories"
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
				if !strings.Contains(rec.Body.String(), "unknown parent category") {
					t.Fatalf("unexpected body %s", rec.Body.String())
				}
			}
		})
	}
}
