package toko

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"
)

// The search migrations run under live traffic (deploy.yml migrates while the old
// binaries serve). golang-migrate runs each file as one implicit transaction, so any
// ACCESS EXCLUSIVE lock is held until the file commits. These tests pin the split:
// 000049 holds only row-count-independent DDL (it does take ACCESS EXCLUSIVE, briefly),
// and the row-proportional work (backfill, GIN builds) lives in 000050, which must never
// take ACCESS EXCLUSIVE on products, so product reads continue while it runs.

const (
	searchUp49   = "migrations/000049_products_search_trgm_simple.up.sql"
	searchUp50   = "migrations/000050_products_search_index_product_sales.up.sql"
	searchDown50 = "migrations/000050_products_search_index_product_sales.down.sql"
)

func TestSearchMigration000049HasNoRowProportionalWork(t *testing.T) {
	body := stripSQLNoise(readMigration(t, searchUp49))
	for _, forbidden := range []string{`(?i)\bUPDATE\s+products\b`, `(?i)\bCREATE\s+(UNIQUE\s+)?INDEX\b`, `(?i)\bVACUUM\b`} {
		if regexp.MustCompile(forbidden).MatchString(body) {
			t.Errorf("%s must not contain %s: it holds ACCESS EXCLUSIVE on products until commit", searchUp49, forbidden)
		}
	}
	// Every ALTER TABLE products must sit inside a catalog-guarded DO block, so a re-run
	// (or the manual large-catalog procedure) takes no lock on products.
	outside := regexp.MustCompile(`(?s)DO \$\$.*?\$\$;`).ReplaceAllString(body, "")
	if regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+products\b`).MatchString(outside) {
		t.Errorf("%s: ALTER TABLE products outside a guarded DO block", searchUp49)
	}
}

func TestSearchMigration000050NeverAltersProducts(t *testing.T) {
	body := stripSQLNoise(readMigration(t, searchUp50))
	if regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+products\b`).MatchString(body) {
		t.Errorf("%s must not ALTER products (ACCESS EXCLUSIVE would block reads for the whole backfill)", searchUp50)
	}
	if regexp.MustCompile(`(?i)\bDROP\s+INDEX\b`).MatchString(body) {
		t.Errorf("%s must not DROP INDEX on products (ACCESS EXCLUSIVE)", searchUp50)
	}
	down := readMigration(t, searchDown50)
	for _, want := range []string{"idx_products_search_simple", "idx_products_title_trgm", "product_sales"} {
		if !strings.Contains(down, want) {
			t.Errorf("%s must drop %s", searchDown50, want)
		}
	}
}

// stripSQLNoise removes comments and single-quoted literals (error messages may name
// statements such as DROP INDEX).
func stripSQLNoise(sql string) string {
	sql = regexp.MustCompile(`(?m)--.*$`).ReplaceAllString(sql, "")
	return regexp.MustCompile(`'[^']*'`).ReplaceAllString(sql, "''")
}

// lockProbe runs a migration file inside an explicit transaction on conn, records the
// locks the transaction holds on the probe schema's products table, checks whether a
// second session can still read products, then commits.
type lockProbe struct {
	t      *testing.T
	conn   *pgx.Conn // runs the migration (search_path = probe schema, public)
	reader *pgx.Conn // concurrent storefront-style reader
	schema string
}

type probeResult struct {
	modes      map[string]bool
	readerErr  error
	tableLocks int
}

func (p lockProbe) run(file string) probeResult {
	p.t.Helper()
	ctx := context.Background()
	tx, err := p.conn.Begin(ctx)
	require.NoError(p.t, err)
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, readMigration(p.t, file))
	require.NoError(p.t, err, file)

	res := probeResult{modes: map[string]bool{}}
	rows, err := tx.Query(ctx,
		`SELECT mode FROM pg_locks WHERE pid = pg_backend_pid() AND locktype = 'relation' AND relation = 'products'::regclass`)
	require.NoError(p.t, err)
	for rows.Next() {
		var mode string
		require.NoError(p.t, rows.Scan(&mode))
		res.modes[mode] = true
		res.tableLocks++
	}
	require.NoError(p.t, rows.Err())

	var n int
	res.readerErr = p.reader.QueryRow(ctx, `SELECT count(*) FROM `+pgx.Identifier{p.schema, "products"}.Sanitize()).Scan(&n)
	require.NoError(p.t, tx.Commit(ctx))
	return res
}

// newSearchProbeSchema creates a throwaway schema holding minimal tenants/products
// copies with 50 products, points conn's search_path at it (pg_trgm still resolves
// from public) and drops it when the test ends.
func newSearchProbeSchema(t *testing.T, ctx context.Context, conn *pgx.Conn) string {
	t.Helper()
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	schema := "mig_lock_probe_" + hex.EncodeToString(b)
	ident := pgx.Identifier{schema}.Sanitize()
	_, err := conn.Exec(ctx, `CREATE SCHEMA `+ident)
	require.NoError(t, err)
	t.Cleanup(func() {
		if _, err := conn.Exec(context.Background(), `DROP SCHEMA `+ident+` CASCADE`); err != nil {
			t.Errorf("drop probe schema: %v", err)
		}
	})
	_, err = conn.Exec(ctx, `SET search_path = `+ident+`, public`)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, `
		CREATE TABLE tenants (id uuid PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE products (
			id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
			tenant_id uuid,
			title text NOT NULL,
			description text,
			search_vector tsvector
		);
		INSERT INTO products (title, description)
		SELECT 'Sepatu ' || g, 'Deskripsi produk ' || g FROM generate_series(1, 50) g;`)
	require.NoError(t, err)
	return schema
}

func isLockTimeout(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "55P03"
}

func TestIntegrationSearchMigrationsLockFootprint(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	reader, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = reader.Close(context.Background()) })

	var hasTrgm bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm')`).Scan(&hasTrgm))
	if !hasTrgm {
		t.Skip("pg_trgm not installed in the test database (apply all migrations first)")
	}

	schema := newSearchProbeSchema(t, ctx, conn)
	_, err = reader.Exec(ctx, `SET lock_timeout = '500ms'`)
	require.NoError(t, err)

	p := lockProbe{t: t, conn: conn, reader: reader, schema: schema}
	countNull := func() int {
		var n int
		require.NoError(t, conn.QueryRow(ctx, `SELECT count(*) FROM products WHERE search_vector_simple IS NULL`).Scan(&n))
		return n
	}
	indexValid := func(name string) (exists, valid bool) {
		err := conn.QueryRow(ctx, `SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
			WHERE i.indrelid = 'products'::regclass AND c.relname = $1`, name).Scan(&valid)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, false
		}
		require.NoError(t, err)
		return true, valid
	}

	// 000049, first run: ACCESS EXCLUSIVE (reads block until commit), but no backfill and
	// no index build happen inside it, so that window is milliseconds.
	r := p.run(searchUp49)
	require.True(t, r.modes["AccessExclusiveLock"], "ADD COLUMN takes ACCESS EXCLUSIVE: %v", r.modes)
	require.True(t, isLockTimeout(r.readerErr), "reads block while 000049 is open: %v", r.readerErr)
	require.Equal(t, 50, countNull(), "000049 must not backfill")
	exists, _ := indexValid("idx_products_search_simple")
	require.False(t, exists, "000049 must not build indexes")

	// 000050, first run: backfill + GIN builds without ACCESS EXCLUSIVE; reads continue.
	r = p.run(searchUp50)
	require.False(t, r.modes["AccessExclusiveLock"], "000050 must not take ACCESS EXCLUSIVE: %v", r.modes)
	require.NoError(t, r.readerErr, "product reads must continue while 000050 runs")
	require.Zero(t, countNull())
	for _, idx := range []string{"idx_products_search_simple", "idx_products_title_trgm"} {
		exists, valid := indexValid(idx)
		require.True(t, exists && valid, idx)
	}

	// Re-runs (and the state the manual large-catalog procedure leaves behind) take no
	// lock on products at all.
	r = p.run(searchUp49)
	require.Zero(t, r.tableLocks, "re-running 000049 must not lock products: %v", r.modes)
	require.NoError(t, r.readerErr)
	r = p.run(searchUp50)
	require.Zero(t, r.tableLocks, "re-running 000050 must not lock products: %v", r.modes)
	require.NoError(t, r.readerErr)

	// Manual path: indexes pre-built, so 000050 skips the backfill even if it would find rows.
	// (The trigger normally prevents NULLs; this only proves the skip is keyed on the index.)
	_, err = conn.Exec(ctx, `ALTER TABLE products DISABLE TRIGGER trg_products_search_vector_simple;
		INSERT INTO products (title) VALUES ('tanpa vektor');
		ALTER TABLE products ENABLE TRIGGER trg_products_search_vector_simple;`)
	require.NoError(t, err)
	require.Equal(t, 1, countNull())
	r = p.run(searchUp50)
	require.Zero(t, r.tableLocks, "000050 with pre-built indexes must not lock products: %v", r.modes)
	require.Equal(t, 1, countNull())

	// A concurrent product write must not deadlock 000050 (which would leave golang-migrate
	// dirty at 50). See searchMigration000050WithConcurrentWriter.
	searchMigration000050WithConcurrentWriter(t, ctx, url)

	// An INVALID index (failed CREATE INDEX CONCURRENTLY) aborts 000050 instead of being kept.
	var super bool
	require.NoError(t, conn.QueryRow(ctx, `SELECT rolsuper FROM pg_roles WHERE rolname = current_user`).Scan(&super))
	if !super {
		t.Log("not a superuser; skipping the INVALID index case")
		return
	}
	_, err = conn.Exec(ctx, `UPDATE pg_index SET indisvalid = false
		WHERE indexrelid = (SELECT oid FROM pg_class WHERE relname = 'idx_products_title_trgm' AND relnamespace = $1::regnamespace)`, schema)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, readMigration(t, searchUp50))
	require.Error(t, err)
	require.Contains(t, err.Error(), "INVALID")
}

// searchMigration000050WithConcurrentWriter runs 000050 against an admin-style
// product writer on a fresh probe schema. The writer holds ROW EXCLUSIVE and a row
// lock on the last heap row when 000050 starts, and once 000050 is waiting it
// updates the first heap row, which a backfill UPDATE would already have row-locked.
// Without the LOCK TABLE ... IN SHARE MODE that precedes the backfill, the two
// sessions wait on each other's row locks (and the later CREATE INDEX on the
// writer's ROW EXCLUSIVE): Postgres aborts one with "deadlock detected". With it,
// 000050 waits for the writer before touching any row, the writer finishes, and
// the migration commits.
func searchMigration000050WithConcurrentWriter(t *testing.T, ctx context.Context, url string) {
	t.Helper()
	migConn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = migConn.Close(context.Background()) })
	writer, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = writer.Close(context.Background()) })

	schema := newSearchProbeSchema(t, ctx, migConn)
	_, err = migConn.Exec(ctx, readMigration(t, searchUp49))
	require.NoError(t, err, searchUp49)
	_, err = writer.Exec(ctx, `SET search_path = `+pgx.Identifier{schema}.Sanitize()+`, public`)
	require.NoError(t, err)
	_, err = writer.Exec(ctx, `SET statement_timeout = '15s'`)
	require.NoError(t, err)

	var firstID, lastID string
	require.NoError(t, writer.QueryRow(ctx, `SELECT id::text FROM products ORDER BY ctid LIMIT 1`).Scan(&firstID))
	require.NoError(t, writer.QueryRow(ctx, `SELECT id::text FROM products ORDER BY ctid DESC LIMIT 1`).Scan(&lastID))

	wtx, err := writer.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = wtx.Rollback(context.Background()) }()
	_, err = wtx.Exec(ctx, `UPDATE products SET title = title || ' (edit)' WHERE id = $1`, lastID)
	require.NoError(t, err)

	migPID := migConn.PgConn().PID()
	migErr := make(chan error, 1)
	go func() {
		tx, err := migConn.Begin(ctx)
		if err != nil {
			migErr <- err
			return
		}
		if _, err := tx.Exec(ctx, readMigration(t, searchUp50)); err != nil {
			_ = tx.Rollback(context.Background())
			migErr <- err
			return
		}
		migErr <- tx.Commit(ctx)
	}()

	// Wait until 000050 is blocked on a lock the writer holds.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		require.NoError(t, writer.QueryRow(ctx,
			`SELECT coalesce(wait_event_type = 'Lock', false) FROM pg_stat_activity WHERE pid = $1`, migPID).Scan(&waiting))
		if waiting {
			break
		}
		select {
		case err := <-migErr:
			t.Fatalf("000050 finished before waiting on the writer: %v", err)
		default:
		}
		require.True(t, time.Now().Before(deadline), "000050 never waited on the writer")
		time.Sleep(20 * time.Millisecond)
	}

	_, err = wtx.Exec(ctx, `UPDATE products SET title = title || ' (edit)' WHERE id = $1`, firstID)
	require.NoError(t, err, "the concurrent product write must not be aborted")
	require.NoError(t, wtx.Commit(ctx))
	require.NoError(t, <-migErr, "000050 must commit while a product write runs concurrently")

	var nulls int
	require.NoError(t, writer.QueryRow(ctx, `SELECT count(*) FROM products WHERE search_vector_simple IS NULL`).Scan(&nulls))
	require.Zero(t, nulls)
	for _, idx := range []string{"idx_products_search_simple", "idx_products_title_trgm"} {
		var valid bool
		require.NoError(t, writer.QueryRow(ctx, `SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
			WHERE i.indrelid = 'products'::regclass AND c.relname = $1`, idx).Scan(&valid))
		require.True(t, valid, idx)
	}
}
