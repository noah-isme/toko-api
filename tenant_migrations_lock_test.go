package toko

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// Migrations 000057-000070 run while the old binaries serve traffic.
// These tests pin their lock discipline: every transactional file starts with
// SET LOCAL lock_timeout, every CREATE/DROP INDEX CONCURRENTLY file is a single
// statement, no file upgrades a table lock after it has row-locked rows, and the
// global unique constraints are only dropped after their per-tenant
// replacements exist.

const (
	variantsUp57 = "migrations/000057_product_variants_tenant_id.up.sql"
	variantsUp58 = "migrations/000058_product_variants_tenant_id_backfill.up.sql"
)

func TestTenantMigrationsLockDiscipline(t *testing.T) {
	entries, err := Migrations.ReadDir("migrations")
	require.NoError(t, err)
	seen := 0
	for _, entry := range entries {
		name := entry.Name()
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil || version < 57 || version > 70 {
			continue
		}
		seen++
		stmts := sqlStatements(readMigration(t, "migrations/"+name))
		if len(stmts) == 0 {
			continue // documented no-op down
		}
		if strings.Contains(strings.ToUpper(stmts[0]), "CONCURRENTLY") {
			require.Len(t, stmts, 1, "%s: a CONCURRENTLY file must be a single statement", name)
			continue
		}
		for _, s := range stmts {
			require.NotContains(t, strings.ToUpper(s), "CONCURRENTLY", "%s: CONCURRENTLY outside a single-statement file", name)
		}
		require.Equal(t, "SET LOCAL lock_timeout = ''", stmts[0], "%s must start with SET LOCAL lock_timeout", name)
	}
	require.Equal(t, 28, seen, "000057-000070 up and down files")

	// Every global constraint is dropped by a file that first checks its
	// per-tenant replacement is valid.
	drops := map[string]string{
		"migrations/000061_product_variants_tenant_id_not_null.up.sql": "product_variants_sku_key",
		"migrations/000063_products_drop_global_slug_unique.up.sql":    "products_slug_key",
		"migrations/000065_categories_drop_global_slug_unique.up.sql":  "categories_slug_key",
		"migrations/000068_brands_drop_global_name_slug_unique.up.sql": "brands_slug_key",
		"migrations/000070_vouchers_drop_global_code_unique.up.sql":    "vouchers_code_key",
	}
	for file, constraint := range drops {
		body := readMigration(t, file)
		check := strings.Index(body, "indisvalid")
		drop := strings.Index(body, "DROP CONSTRAINT IF EXISTS "+constraint)
		require.True(t, check > 0 && drop > check, "%s must verify the per-tenant index before dropping %s", file, constraint)
	}

	// The backfills lock their table before touching rows.
	for file, lock := range map[string]string{
		variantsUp58: "LOCK TABLE product_variants IN EXCLUSIVE MODE",
	} {
		stmts := sqlStatements(readMigration(t, file))
		require.GreaterOrEqual(t, len(stmts), 3, file)
		require.Equal(t, lock, stmts[1], "%s: the lock must be the first statement after lock_timeout", file)
	}
}

// TestIntegrationVariantsBackfill000058ConcurrentCatalogWriter runs 000058
// against a catalog writer that holds a variant row lock (FOR NO KEY UPDATE,
// as bulk/import do; it takes only ROW SHARE) when the migration starts and
// then updates a second variant the backfill would already have row-locked.
// SHARE ROW EXCLUSIVE would admit the writer's ROW SHARE, the backfill would
// wait on the writer's row and the writer's UPDATE on the backfill's table
// lock: a deadlock. EXCLUSIVE makes the backfill wait for the writer first.
func TestIntegrationVariantsBackfill000058ConcurrentCatalogWriter(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	migConn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = migConn.Close(context.Background()) })
	writer, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close(context.Background()) })

	schema := newProbeSchema(t, ctx, migConn, "mig_variants_probe_", `
		CREATE TABLE tenants (id uuid PRIMARY KEY);
		CREATE TABLE products (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid NOT NULL REFERENCES tenants(id));
		CREATE TABLE product_variants (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			sku text UNIQUE,
			stock int NOT NULL DEFAULT 0
		);
		INSERT INTO tenants VALUES ('00000000-0000-0000-0000-000000000001'), ('00000000-0000-0000-0000-000000000002');
		INSERT INTO products (tenant_id) SELECT CASE WHEN g % 2 = 0 THEN '00000000-0000-0000-0000-000000000001'::uuid ELSE '00000000-0000-0000-0000-000000000002'::uuid END FROM generate_series(1, 20) g;
		INSERT INTO product_variants (product_id, sku, stock) SELECT id, 'SKU-' || row_number() OVER (), 5 FROM products;`)
	_, err = migConn.Exec(ctx, readMigration(t, variantsUp57))
	require.NoError(t, err, variantsUp57)
	_, err = writer.Exec(ctx, `SET search_path = `+pgx.Identifier{schema}.Sanitize()+`, public`)
	require.NoError(t, err)
	_, err = writer.Exec(ctx, `SET statement_timeout = '15s'`)
	require.NoError(t, err)

	var firstID, lastID string
	require.NoError(t, writer.QueryRow(ctx, `SELECT id::text FROM product_variants ORDER BY ctid LIMIT 1`).Scan(&firstID))
	require.NoError(t, writer.QueryRow(ctx, `SELECT id::text FROM product_variants ORDER BY ctid DESC LIMIT 1`).Scan(&lastID))

	wtx, err := writer.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = wtx.Rollback(context.Background()) }()
	_, err = wtx.Exec(ctx, `SELECT 1 FROM product_variants WHERE id = $1 FOR NO KEY UPDATE`, lastID)
	require.NoError(t, err)

	done := runMigrationAsync(ctx, migConn, readMigration(t, variantsUp58))
	waitForLock(t, ctx, writer, migConn.PgConn().PID(), done, "000058")

	_, err = wtx.Exec(ctx, `UPDATE product_variants SET stock = stock - 1 WHERE id = ANY($1::uuid[])`, []string{firstID, lastID})
	require.NoError(t, err, "the concurrent catalog write must not be aborted")
	require.NoError(t, wtx.Commit(ctx))
	require.NoError(t, <-done, "000058 must commit while a catalog write runs concurrently")

	var missing, mismatched int
	require.NoError(t, writer.QueryRow(ctx, `SELECT count(*) FILTER (WHERE v.tenant_id IS NULL), count(*) FILTER (WHERE v.tenant_id <> p.tenant_id)
		FROM product_variants v JOIN products p ON p.id = v.product_id`).Scan(&missing, &mismatched))
	require.Zero(t, missing)
	require.Zero(t, mismatched)

	// New rows get the product's tenant from the 000057 trigger, whatever the caller passes.
	var tenantID string
	require.NoError(t, writer.QueryRow(ctx, `INSERT INTO product_variants (product_id, sku, tenant_id)
		SELECT id, 'NEW-1', '00000000-0000-0000-0000-000000000002' FROM products WHERE tenant_id = '00000000-0000-0000-0000-000000000001' LIMIT 1
		RETURNING tenant_id::text`).Scan(&tenantID))
	require.Equal(t, "00000000-0000-0000-0000-000000000001", tenantID)
}
