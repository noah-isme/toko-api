package notifications_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/notifications"
)

// These tests prove the CreateNotification ON CONFLICT clause matches the
// partial unique index from migration 000048 (a mismatch fails with "no unique
// or exclusion constraint matching the ON CONFLICT specification"). They are
// opt-in: set TEST_DATABASE_URL to a migrated database to run them.

type notifEnv struct {
	pool   *pgxpool.Pool
	q      *dbgen.Queries
	tenant pgtype.UUID
	users  []pgtype.UUID
}

func randHex() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newNotifEnv(t *testing.T, userCount int) *notifEnv {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	require.NoError(t, pool.Ping(ctx))

	env := &notifEnv{pool: pool, q: dbgen.New(pool)}
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO tenants(slug,name) VALUES($1,$2) RETURNING id`, "it-notif-"+randHex(), "IT notif").Scan(&env.tenant))
	for i := 0; i < userCount; i++ {
		var id pgtype.UUID
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO users(name,email,password_hash) VALUES($1,$2,$3) RETURNING id`,
			"IT user", "it-notif-"+randHex()+"@example.test", "x").Scan(&id))
		env.users = append(env.users, id)
	}
	// Registered after pool.Close so it runs first.
	t.Cleanup(func() {
		cctx := context.Background()
		for _, u := range env.users {
			_, _ = pool.Exec(cctx, `DELETE FROM notifications WHERE user_id = $1`, u)
			_, _ = pool.Exec(cctx, `DELETE FROM users WHERE id = $1`, u)
		}
		_, _ = pool.Exec(cctx, `DELETE FROM tenants WHERE id = $1`, env.tenant)
	})
	return env
}

func (e *notifEnv) create(userID, eventID pgtype.UUID) (dbgen.Notification, error) {
	return e.q.CreateNotification(context.Background(), dbgen.CreateNotificationParams{
		UserID: userID, TenantID: e.tenant, Type: "order_paid", Title: "t", Body: "b", Data: []byte(`{}`), EventID: eventID,
	})
}

func (e *notifEnv) count(t *testing.T, where string, arg any) int {
	t.Helper()
	var n int
	require.NoError(t, e.pool.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE `+where, arg).Scan(&n))
	return n
}

func TestIntegrationCreateNotificationDedupesByEventAndUser(t *testing.T) {
	env := newNotifEnv(t, 2)
	eventID := pgtype.UUID{Bytes: uuid.New(), Valid: true}

	first, err := env.create(env.users[0], eventID)
	require.NoError(t, err)
	require.Equal(t, eventID, first.EventID)

	_, err = env.create(env.users[0], eventID)
	require.True(t, errors.Is(err, pgx.ErrNoRows), "duplicate should hit ON CONFLICT DO NOTHING, got %v", err)
	require.Equal(t, 1, env.count(t, "user_id = $1", env.users[0]))

	_, err = env.create(env.users[1], eventID)
	require.NoError(t, err, "same event for another user is a distinct notification")
	require.Equal(t, 2, env.count(t, "event_id = $1", eventID))

	svc := &notifications.Service{Q: env.q}
	dup, err := svc.Create(context.Background(), env.users[0], env.tenant, eventID, "order_paid", "t", "b", nil)
	require.NoError(t, err, "Service.Create maps the conflict to a nil error")
	require.False(t, dup.ID.Valid)
}

func TestIntegrationCreateNotificationAllowsNullEventID(t *testing.T) {
	env := newNotifEnv(t, 1)
	_, err := env.create(env.users[0], pgtype.UUID{})
	require.NoError(t, err)
	_, err = env.create(env.users[0], pgtype.UUID{})
	require.NoError(t, err)
	require.Equal(t, 2, env.count(t, "user_id = $1", env.users[0]))
}
