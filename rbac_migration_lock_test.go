package toko

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

// Migration 000055 runs while the old api reads tenant_memberships on every
// authenticated request. These tests pin that it takes ACCESS EXCLUSIVE before
// its backfill, so it never upgrades a lock after row-locking rows.

const (
	rbacUp55   = "migrations/000055_tenant_membership_staff_roles.up.sql"
	rbacDown55 = "migrations/000055_tenant_membership_staff_roles.down.sql"
)

// sqlStatements splits a migration into statements, ignoring comments,
// literals and dollar-quoted bodies (DO blocks, functions).
func sqlStatements(sql string) []string {
	sql = regexp.MustCompile(`(?s)\$\$.*?\$\$`).ReplaceAllString(sql, "$$$$")
	sql = stripSQLNoise(sql)
	var out []string
	for _, s := range strings.Split(sql, ";") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func TestRBACMigration000055LocksBeforeBackfill(t *testing.T) {
	for _, file := range []string{rbacUp55, rbacDown55} {
		stmts := sqlStatements(readMigration(t, file))
		require.GreaterOrEqual(t, len(stmts), 3, file)
		require.Equal(t, "SET LOCAL lock_timeout = ''", stmts[0], file)
		require.Equal(t, "LOCK TABLE tenant_memberships IN ACCESS EXCLUSIVE MODE", stmts[1], "%s: the lock must come before any UPDATE", file)
	}
}

// waitForLock blocks until the backend pid waits on a heavyweight lock, or
// fails when done reports that the migration finished first.
func waitForLock(t *testing.T, ctx context.Context, conn *pgx.Conn, pid uint32, done <-chan error, what string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting bool
		require.NoError(t, conn.QueryRow(ctx,
			`SELECT coalesce(wait_event_type = 'Lock', false) FROM pg_stat_activity WHERE pid = $1`, pid).Scan(&waiting))
		if waiting {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("%s finished before waiting on the concurrent session: %v", what, err)
		default:
		}
		require.True(t, time.Now().Before(deadline), "%s never waited on the concurrent session", what)
		time.Sleep(20 * time.Millisecond)
	}
}

func runMigrationAsync(ctx context.Context, conn *pgx.Conn, sql string) <-chan error {
	done := make(chan error, 1)
	go func() {
		tx, err := conn.Begin(ctx)
		if err != nil {
			done <- err
			return
		}
		if _, err := tx.Exec(ctx, sql); err != nil {
			_ = tx.Rollback(context.Background())
			done <- err
			return
		}
		done <- tx.Commit(ctx)
	}()
	return done
}

func newProbeSchema(t *testing.T, ctx context.Context, conn *pgx.Conn, prefix, ddl string) string {
	t.Helper()
	schema := prefix + randProbeSuffix()
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
	_, err = conn.Exec(ctx, ddl)
	require.NoError(t, err)
	return schema
}

func randProbeSuffix() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// TestIntegrationRBACMigration000055ConcurrentStaffChange runs 000055 against a
// session that read tenant_memberships earlier in its transaction and then
// changes a row the backfill targets (a staff change: SELECT ... FOR UPDATE,
// then UPDATE). Without an up-front ACCESS EXCLUSIVE lock the backfill
// row-locks that row while the ALTER waits for the session's ACCESS SHARE, the
// session waits on the row: "deadlock detected", and golang-migrate stops dirty
// at 55. SHARE ROW EXCLUSIVE does not help (the session's UPDATE would wait on
// it while the ALTER waits on the session). With ACCESS EXCLUSIVE first, the
// migration waits for the session before touching any row and both commit.
func TestIntegrationRBACMigration000055ConcurrentStaffChange(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	migConn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = migConn.Close(context.Background()) })
	session, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close(context.Background()) })

	schema := newProbeSchema(t, ctx, migConn, "mig_rbac_probe_", `
		CREATE TABLE users (id uuid PRIMARY KEY DEFAULT gen_random_uuid(), roles text[] NOT NULL DEFAULT '{customer}');
		CREATE TABLE tenant_memberships (
			tenant_id uuid NOT NULL,
			user_id uuid NOT NULL REFERENCES users(id),
			role text NOT NULL DEFAULT 'CUSTOMER',
			status text NOT NULL DEFAULT 'ACTIVE',
			updated_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (tenant_id, user_id),
			CONSTRAINT tenant_memberships_role_check CHECK (role IN ('OWNER','ADMIN','CUSTOMER'))
		);
		INSERT INTO users (id, roles) VALUES
			('00000000-0000-0000-0000-00000000000a', '{admin}'),
			('00000000-0000-0000-0000-00000000000b', '{customer}');
		INSERT INTO tenant_memberships (tenant_id, user_id) VALUES
			('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-00000000000a'),
			('00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-00000000000b');`)
	_, err = session.Exec(ctx, `SET search_path = `+pgx.Identifier{schema}.Sanitize()+`, public`)
	require.NoError(t, err)
	_, err = session.Exec(ctx, `SET statement_timeout = '15s'`)
	require.NoError(t, err)

	stx, err := session.Begin(ctx)
	require.NoError(t, err)
	defer func() { _ = stx.Rollback(context.Background()) }()
	var role string
	require.NoError(t, stx.QueryRow(ctx, `SELECT role FROM tenant_memberships WHERE user_id = '00000000-0000-0000-0000-00000000000b'`).Scan(&role))

	done := runMigrationAsync(ctx, migConn, readMigration(t, rbacUp55))
	waitForLock(t, ctx, session, migConn.PgConn().PID(), done, "000055")

	// The admin's membership is exactly the row the backfill updates.
	_, err = stx.Exec(ctx, `SELECT 1 FROM tenant_memberships WHERE user_id = '00000000-0000-0000-0000-00000000000a' FOR UPDATE`)
	require.NoError(t, err, "the concurrent staff change must not be aborted")
	_, err = stx.Exec(ctx, `UPDATE tenant_memberships SET status = 'SUSPENDED', updated_at = now() WHERE user_id = '00000000-0000-0000-0000-00000000000a'`)
	require.NoError(t, err, "the concurrent staff change must not be aborted")
	require.NoError(t, stx.Commit(ctx))
	require.NoError(t, <-done, "000055 must commit while a staff change runs concurrently")

	var status string
	require.NoError(t, session.QueryRow(ctx, `SELECT role, status FROM tenant_memberships WHERE user_id = '00000000-0000-0000-0000-00000000000a'`).Scan(&role, &status))
	require.Equal(t, "ADMIN", role, "backfill applied after the concurrent change")
	require.Equal(t, "SUSPENDED", status)
	var def string
	require.NoError(t, session.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'tenant_memberships_role_check' AND conrelid = 'tenant_memberships'::regclass`).Scan(&def))
	require.Contains(t, def, "MANAGER")
}
