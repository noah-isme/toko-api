package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeQueryFile(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckFileCatchesMultilineAdminReadWithoutTenant(t *testing.T) {
	path := writeQueryFile(t, "admin_orders.sql", `-- name: AdminListOrders :many
SELECT o.id,
       o.status
FROM orders o
WHERE o.status = $1;
`)

	ok, err := checkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected an unscoped admin query to fail")
	}
}

func TestCheckFileAllowsTenantScopedAdminRead(t *testing.T) {
	path := writeQueryFile(t, "admin_orders.sql", `-- name: AdminListOrders :many
SELECT o.id,
       o.status
FROM orders o
WHERE o.tenant_id = sqlc.arg(tenant_id)
  AND o.status = $1;
`)

	ok, err := checkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected a tenant-scoped admin query to pass")
	}
}

func TestCheckFileCatchesAdminUpdateAndDelete(t *testing.T) {
	path := writeQueryFile(t, "admin_vouchers.sql", `-- name: AdminUpdateVoucher :exec
UPDATE vouchers
SET used_count = used_count + 1
WHERE code = $1;

-- name: AdminDeleteVoucher :exec
DELETE FROM vouchers
WHERE code = $1;
`)

	ok, err := checkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected unscoped admin update/delete queries to fail")
	}
}

func TestCheckFileLeavesNonAdminQueriesOutOfBound(t *testing.T) {
	path := writeQueryFile(t, "orders.sql", `-- name: GetOrderByID :one
SELECT *
FROM orders
WHERE id = $1;
`)

	ok, err := checkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected non-admin query files to remain outside this bounded check")
	}
}
