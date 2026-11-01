package loyalty_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
	"github.com/noah-isme/backend-toko/internal/loyalty"
)

// Opt-in: set TEST_DATABASE_URL to a migrated database to run it.
// UpdateProfilePoints runs UpdateLoyaltyProfilePoints against the real schema,
// which once named a loyalty_profiles column that does not exist.
func TestIntegrationUpdateProfilePoints(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	svc := &loyalty.Service{Q: dbgen.New(pool)}

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	var user pgtype.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users(name,email,password_hash) VALUES('IT','it-loyalty-'||$1||'@example.test','x') RETURNING id`,
		hex.EncodeToString(b)).Scan(&user))
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user) })

	_, err = svc.CreateOrUpdateProfile(ctx, user, 100, 0, 100, "bronze")
	require.NoError(t, err)

	updated, err := svc.UpdateProfilePoints(ctx, user, 40, 160)
	require.NoError(t, err)
	require.Equal(t, int32(40), updated.Points)
	require.Equal(t, int32(160), updated.LifetimePoints)

	profile, err := svc.GetProfile(ctx, user)
	require.NoError(t, err)
	require.Equal(t, int32(40), profile.Points)
	require.Equal(t, int32(160), profile.LifetimePoints)
}
