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
	drops := []struct{ file, constraint string }{
		{"migrations/000061_product_variants_tenant_id_not_null.up.sql", "product_variants_sku_key"},
		{"migrations/000063_products_drop_global_slug_unique.up.sql", "products_slug_key"},
		{"migrations/000065_categories_drop_global_slug_unique.up.sql", "categories_slug_key"},
		{"migrations/000068_brands_drop_global_name_slug_unique.up.sql", "brands_name_key"},
		{"migrations/000068_brands_drop_global_name_slug_unique.up.sql", "brands_slug_key"},
		{"migrations/000070_vouchers_drop_global_code_unique.up.sql", "vouchers_code_key"},
	}
	for _, d := range drops {
		body := readMigration(t, d.file)
		check := strings.Index(body, "indisvalid")
		drop := strings.Index(body, "DROP CONSTRAINT IF EXISTS "+d.constraint)
		require.True(t, check > 0 && drop > check, "%s must verify the per-tenant index before dropping %s", d.file, d.constraint)
	}

	// The 000058 backfill first runs its large-catalog guard (a DO block that takes
	// only ACCESS SHARE and counts at most 50001 rows), then locks its table before touching rows.
	body := readMigration(t, variantsUp58)
	stmts := sqlStatements(body)
	require.GreaterOrEqual(t, len(stmts), 4, variantsUp58)
	require.Equal(t, "DO $$", stmts[1], "%s: the large-catalog guard must follow lock_timeout", variantsUp58)
	require.Equal(t, "LOCK TABLE product_variants IN EXCLUSIVE MODE", stmts[2], "%s: the lock must follow the guard, before any UPDATE", variantsUp58)
	guard := strings.Index(body, "LIMIT 50001")
	lock := strings.Index(body, "LOCK TABLE")
	require.True(t, guard > 0 && guard < lock, "%s: the guard must run its bounded count before the LOCK", variantsUp58)
	require.Contains(t, body, "docs/ops/RUNBOOK.md", "%s: the guard must point at the RUNBOOK", variantsUp58)
	require.True(t, strings.HasPrefix(stmts[3], "UPDATE product_variants"), variantsUp58)
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

// variantsKeysetBackfill is the manual large-catalog backfill from
// docs/ops/RUNBOOK.md (Migrations 000057-000070), verbatim: keyset batches by
// id, each committed on its own so row locks are held for one batch only.
const variantsKeysetBackfill = `DO $$
DECLARE
  last_id uuid := '00000000-0000-0000-0000-000000000000';
  next_id uuid;
BEGIN
  LOOP
    SELECT id INTO next_id
    FROM (SELECT id FROM product_variants WHERE id > last_id ORDER BY id LIMIT 1000) batch
    ORDER BY id DESC LIMIT 1;
    EXIT WHEN next_id IS NULL;
    UPDATE product_variants v SET tenant_id = p.tenant_id
    FROM products p
    WHERE p.id = v.product_id AND v.id > last_id AND v.id <= next_id AND v.tenant_id IS NULL;
    COMMIT;
    last_id := next_id;
  END LOOP;
END;
$$;`

// TestIntegrationVariantsBackfill000058LargeCatalogGuard pins 000058's safety
// default: above 50k variants (bounded exact count, so it also holds on a
// never-analyzed table) with rows still to backfill it fails before taking its
// EXCLUSIVE lock and points at the RUNBOOK; after the RUNBOOK's keyset backfill
// it passes.
func TestIntegrationVariantsBackfill000058LargeCatalogGuard(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	runbook, err := os.ReadFile("docs/ops/RUNBOOK.md")
	require.NoError(t, err)
	require.Contains(t, string(runbook), variantsKeysetBackfill, "the RUNBOOK's manual backfill must match the tested one")

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	newProbeSchema(t, ctx, conn, "mig_variants_guard_", `
		CREATE TABLE tenants (id uuid PRIMARY KEY);
		CREATE TABLE products (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), tenant_id uuid NOT NULL REFERENCES tenants(id));
		CREATE TABLE product_variants (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			sku text UNIQUE,
			stock int NOT NULL DEFAULT 0
		) WITH (autovacuum_enabled = false);
		INSERT INTO tenants VALUES ('00000000-0000-0000-0000-000000000001'), ('00000000-0000-0000-0000-000000000002');
		INSERT INTO products (tenant_id) SELECT CASE WHEN g % 2 = 0 THEN '00000000-0000-0000-0000-000000000001'::uuid ELSE '00000000-0000-0000-0000-000000000002'::uuid END FROM generate_series(1, 1000) g;
		INSERT INTO product_variants (product_id, sku) SELECT p.id, 'SKU-' || g FROM generate_series(1, 50001) g JOIN LATERAL (SELECT id FROM products OFFSET g % 1000 LIMIT 1) p ON true;
		`)
	// Never analyzed (autovacuum is off for the table): pg_class.reltuples is 0 or -1
	// here, so a guard that trusted it would let 000058 take its EXCLUSIVE lock.
	var reltuples float32
	require.NoError(t, conn.QueryRow(ctx, `SELECT reltuples FROM pg_class WHERE oid = 'product_variants'::regclass`).Scan(&reltuples))
	require.LessOrEqual(t, reltuples, float32(0), "the table must be never-analyzed for this case")
	_, err = conn.Exec(ctx, readMigration(t, variantsUp57))
	require.NoError(t, err, variantsUp57)

	err = <-runMigrationAsync(ctx, conn, readMigration(t, variantsUp58))
	require.Error(t, err, "000058 must refuse a large un-backfilled catalog")
	require.Contains(t, err.Error(), "docs/ops/RUNBOOK.md")

	_, err = conn.Exec(ctx, variantsKeysetBackfill)
	require.NoError(t, err, "RUNBOOK keyset backfill")
	var missing, mismatched int
	require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE v.tenant_id IS NULL), count(*) FILTER (WHERE v.tenant_id <> p.tenant_id)
		FROM product_variants v JOIN products p ON p.id = v.product_id`).Scan(&missing, &mismatched))
	require.Zero(t, missing)
	require.Zero(t, mismatched)

	require.NoError(t, <-runMigrationAsync(ctx, conn, readMigration(t, variantsUp58)), "000058 must pass once the RUNBOOK backfill is done")
}

// TestIntegrationVariantsTenantTrigger000057UpdatePaths pins the 000057 UPDATE
// trigger: an explicit tenant change is reverted, a product_id change re-derives
// the tenant, a NULL tenant is filled by any update, and an ordinary stock update
// of a row that already has its tenant never calls the plpgsql function.
func TestIntegrationVariantsTenantTrigger000057UpdatePaths(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	const t1, t2 = "00000000-0000-0000-0000-000000000001", "00000000-0000-0000-0000-000000000002"
	const p1, p2 = "00000000-0000-0000-0000-0000000000a1", "00000000-0000-0000-0000-0000000000a2"
	const legacy, filled = "00000000-0000-0000-0000-0000000000b1", "00000000-0000-0000-0000-0000000000b2"
	newProbeSchema(t, ctx, conn, "mig_variants_trigger_", `
		CREATE TABLE tenants (id uuid PRIMARY KEY);
		CREATE TABLE products (id uuid PRIMARY KEY, tenant_id uuid NOT NULL REFERENCES tenants(id));
		CREATE TABLE product_variants (
			id uuid PRIMARY KEY,
			product_id uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
			stock int NOT NULL DEFAULT 0
		);
		INSERT INTO tenants VALUES ('`+t1+`'), ('`+t2+`');
		INSERT INTO products VALUES ('`+p1+`', '`+t1+`'), ('`+p2+`', '`+t2+`');
		INSERT INTO product_variants (id, product_id, stock) VALUES ('`+legacy+`', '`+p1+`', 5);`)
	_, err = conn.Exec(ctx, readMigration(t, variantsUp57))
	require.NoError(t, err, variantsUp57)

	tenantOf := func(id string) *string {
		var tenant *string
		require.NoError(t, conn.QueryRow(ctx, `SELECT tenant_id::text FROM product_variants WHERE id = $1`, id).Scan(&tenant))
		return tenant
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := conn.Exec(ctx, sql, args...)
		require.NoError(t, err, sql)
	}

	// Written before 000057: NULL until any update touches it.
	require.Nil(t, tenantOf(legacy))
	exec(`UPDATE product_variants SET stock = stock - 1 WHERE id = $1`, legacy)
	require.Equal(t, t1, *tenantOf(legacy), "a stock update fills a NULL tenant")

	// Insert derives the tenant, whatever the caller passes.
	exec(`INSERT INTO product_variants (id, product_id, tenant_id) VALUES ($1, $2, $3)`, filled, p1, t2)
	require.Equal(t, t1, *tenantOf(filled))

	exec(`UPDATE product_variants SET tenant_id = $2 WHERE id = $1`, filled, t2)
	require.Equal(t, t1, *tenantOf(filled), "an explicit tenant change is reverted to the product's tenant")

	exec(`UPDATE product_variants SET tenant_id = NULL WHERE id = $1`, filled)
	require.Equal(t, t1, *tenantOf(filled), "an explicit NULL is re-filled")

	exec(`UPDATE product_variants SET product_id = $2 WHERE id = $1`, filled, p2)
	require.Equal(t, t2, *tenantOf(filled), "a product_id change re-derives the tenant")

	exec(`UPDATE product_variants SET product_id = $2, tenant_id = $3 WHERE id = $1`, filled, p1, t2)
	require.Equal(t, t1, *tenantOf(filled), "product and tenant changed together follow the product")

	// Hot path: the WHEN clause keeps ordinary updates out of plpgsql.
	var super bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super))
	if !super {
		t.Log("not a superuser (track_functions); skipping the call-count check")
		return
	}
	calls := func(tx pgx.Tx) int64 {
		var n int64
		require.NoError(t, tx.QueryRow(ctx, `SELECT coalesce(sum(calls), 0)::bigint FROM pg_stat_xact_user_functions
			WHERE funcid = 'product_variants_set_tenant_id'::regproc`).Scan(&n))
		return n
	}
	tx, err := conn.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, `SET LOCAL track_functions = 'pl'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE product_variants SET stock = stock - 1`)
	require.NoError(t, err)
	require.Zero(t, calls(tx), "a stock update of rows with a tenant must not call the trigger function")
	_, err = tx.Exec(ctx, `UPDATE product_variants SET product_id = $2 WHERE id = $1`, filled, p2)
	require.NoError(t, err)
	require.Equal(t, int64(1), calls(tx), "a product_id change calls it once")
}
