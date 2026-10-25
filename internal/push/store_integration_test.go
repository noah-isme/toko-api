package push_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// Opt-in: set TEST_DATABASE_URL to a migrated database to run it.
func TestIntegrationTrimPushSubscriptionsKeepsNewest(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	q := dbgen.New(pool)

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	var user pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users(name,email,password_hash) VALUES('IT','it-push-'||$1||'@example.test','x') RETURNING id`,
		hex.EncodeToString(b)).Scan(&user))
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user) })

	for i := 0; i < 4; i++ {
		_, err := q.CreatePushSubscription(ctx, dbgen.CreatePushSubscriptionParams{
			UserID: user, Endpoint: fmt.Sprintf("https://fcm.googleapis.com/fcm/send/%d", i), P256dh: "p", Auth: "a",
		})
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `UPDATE push_subscriptions SET created_at = $2 WHERE user_id = $1 AND endpoint = $3`,
			user, time.Now().Add(time.Duration(i)*time.Minute), fmt.Sprintf("https://fcm.googleapis.com/fcm/send/%d", i))
		require.NoError(t, err)
	}
	require.NoError(t, q.TrimPushSubscriptions(ctx, dbgen.TrimPushSubscriptionsParams{UserID: user, Offset: 2}))
	subs, err := q.GetPushSubscriptionsByUser(ctx, user)
	require.NoError(t, err)
	var endpoints []string
	for _, s := range subs {
		endpoints = append(endpoints, s.Endpoint)
	}
	require.ElementsMatch(t, []string{"https://fcm.googleapis.com/fcm/send/2", "https://fcm.googleapis.com/fcm/send/3"}, endpoints)
}
