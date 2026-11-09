package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/noah-isme/backend-toko/internal/common"
	"github.com/noah-isme/backend-toko/internal/rbac"
)

func meRequest(t *testing.T, handler *Handler, userID string, role *rbac.Role) map[string]json.RawMessage {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	ctx := common.WithUserID(req.Context(), userID)
	if role != nil {
		ctx = rbac.WithRole(ctx, *role)
	}
	rec := httptest.NewRecorder()
	handler.Me(rec, req.WithContext(ctx))
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body.Data
}

func TestMeIncludesRoleAndPermissions(t *testing.T) {
	queries := newFakeQueries()
	userID := seedUser(t, queries, "me-role@example.com")
	handler := newTestHandler(t, queries, &common.InMemoryEmail{})

	manager := rbac.RoleManager
	data := meRequest(t, handler, userID, &manager)
	var role string
	var perms []string
	if err := json.Unmarshal(data["role"], &role); err != nil || role != "manager" {
		t.Fatalf("role = %s (%v)", data["role"], err)
	}
	if err := json.Unmarshal(data["permissions"], &perms); err != nil {
		t.Fatalf("permissions: %v", err)
	}
	want := rbac.PermissionStrings(rbac.RoleManager)
	if len(perms) != len(want) {
		t.Fatalf("permissions = %v, want %v", perms, want)
	}
	for i := range want {
		if perms[i] != want[i] {
			t.Fatalf("permissions = %v, want %v", perms, want)
		}
	}
	if _, ok := data["roles"]; !ok {
		t.Fatal("legacy roles must still be returned")
	}

	customer := rbac.RoleCustomer
	data = meRequest(t, handler, userID, &customer)
	if string(data["role"]) != `"customer"` || string(data["permissions"]) != `[]` {
		t.Fatalf("customer role=%s permissions=%s", data["role"], data["permissions"])
	}

	data = meRequest(t, handler, userID, nil)
	if _, ok := data["role"]; ok {
		t.Fatalf("role must be absent without a tenant role, got %s", data["role"])
	}
	if _, ok := data["permissions"]; ok {
		t.Fatalf("permissions must be absent without a tenant role, got %s", data["permissions"])
	}
}
