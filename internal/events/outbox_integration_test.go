package events_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// These tests run the outbox SQL against a real Postgres with all migrations
// applied. They are opt-in: set TEST_DATABASE_URL to run them.

func newOutboxPool(t *testing.T) (*pgxpool.Pool, string) {
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

	b := make([]byte, 6)
	_, _ = rand.Read(b)
	topic := "itest." + hex.EncodeToString(b)
	// Registered after pool.Close so it runs first.
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM domain_events WHERE topic = $1`, topic); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	return pool, topic
}

func insertEvent(t *testing.T, q *dbgen.Queries, topic string) dbgen.DomainEvent {
	t.Helper()
	ev, err := q.InsertDomainEvent(context.Background(), dbgen.InsertDomainEventParams{
		Topic:       topic,
		AggregateID: pgtype.UUID{Bytes: uuid.New(), Valid: true},
		Payload:     []byte(`{"k":"v"}`),
	})
	require.NoError(t, err)
	require.False(t, ev.DispatchedAt.Valid, "new events start undispatched")
	return ev
}

func setAge(t *testing.T, pool *pgxpool.Pool, id pgtype.UUID, age string) {
	t.Helper()
	_, err := pool.Exec(context.Background(), `UPDATE domain_events SET occurred_at = now() - $2::interval WHERE id = $1`, id, age)
	require.NoError(t, err)
}

func listPending(t *testing.T, q *dbgen.Queries, topic string, minAge time.Duration, limit int32) []dbgen.DomainEvent {
	t.Helper()
	now := time.Now()
	rows, err := q.ListUndispatchedDomainEvents(context.Background(), dbgen.ListUndispatchedDomainEventsParams{
		Topics:      []string{topic},
		NotBefore:   pgtype.Timestamptz{Time: now.Add(-24 * time.Hour), Valid: true},
		OlderThan:   pgtype.Timestamptz{Time: now.Add(-minAge), Valid: true},
		MaxAttempts: 10,
		BatchSize:   limit,
	})
	require.NoError(t, err)
	return rows
}

func ids(rows []dbgen.DomainEvent) []pgtype.UUID {
	out := make([]pgtype.UUID, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

func TestIntegrationListUndispatchedFiltersAndOrders(t *testing.T) {
	pool, topic := newOutboxPool(t)
	q := dbgen.New(pool)
	ctx := context.Background()

	ev1 := insertEvent(t, q, topic)
	ev2 := insertEvent(t, q, topic)
	ev3 := insertEvent(t, q, topic)
	evOld := insertEvent(t, q, topic)
	setAge(t, pool, ev1.ID, "2 minutes")
	setAge(t, pool, ev2.ID, "1 minute")
	setAge(t, pool, ev3.ID, "0 seconds")
	setAge(t, pool, evOld.ID, "3 days") // outside the 24h window

	require.Equal(t, []pgtype.UUID{ev1.ID, ev2.ID}, ids(listPending(t, q, topic, 30*time.Second, 100)))
	require.Len(t, listPending(t, q, topic, 30*time.Second, 1), 1)

	require.NoError(t, q.RecordDomainEventDispatchFailure(ctx, dbgen.RecordDomainEventDispatchFailureParams{
		ID: ev1.ID, LastDispatchError: pgtype.Text{String: "boom", Valid: true},
	}))
	got, err := q.GetDomainEvent(ctx, ev1.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), got.DispatchAttempts)
	require.Equal(t, "boom", got.LastDispatchError.String)
	require.False(t, got.DispatchedAt.Valid)

	_, err = pool.Exec(ctx, `UPDATE domain_events SET dispatch_attempts = 10 WHERE id = $1`, ev1.ID)
	require.NoError(t, err)
	require.Equal(t, []pgtype.UUID{ev2.ID}, ids(listPending(t, q, topic, 30*time.Second, 100)))

	require.NoError(t, q.RecordDomainEventDispatchFailure(ctx, dbgen.RecordDomainEventDispatchFailureParams{
		ID: ev2.ID, LastDispatchError: pgtype.Text{String: "transient", Valid: true},
	}))
	require.NoError(t, q.MarkDomainEventDispatched(ctx, ev2.ID))
	got, err = q.GetDomainEvent(ctx, ev2.ID)
	require.NoError(t, err)
	require.True(t, got.DispatchedAt.Valid)
	require.False(t, got.LastDispatchError.Valid)
	require.Empty(t, listPending(t, q, topic, 30*time.Second, 100))
}

func TestIntegrationOldBinaryInsertIsNeverListed(t *testing.T) {
	pool, topic := newOutboxPool(t)
	q := dbgen.New(pool)
	ctx := context.Background()

	// The pre-000047 InsertDomainEvent statement, which does not name
	// dispatched_at: the column default marks the row dispatched.
	var oldID pgtype.UUID
	err := pool.QueryRow(ctx,
		`INSERT INTO domain_events (topic, aggregate_id, payload) VALUES ($1, $2, '{}') RETURNING id`,
		topic, pgtype.UUID{Bytes: uuid.New(), Valid: true}).Scan(&oldID)
	require.NoError(t, err)
	setAge(t, pool, oldID, "1 hour")
	old, err := q.GetDomainEvent(ctx, oldID)
	require.NoError(t, err)
	require.True(t, old.DispatchedAt.Valid)

	fresh := insertEvent(t, q, topic)
	setAge(t, pool, fresh.ID, "1 hour")

	require.Equal(t, []pgtype.UUID{fresh.ID}, ids(listPending(t, q, topic, 0, 100)))
}

func TestIntegrationListUndispatchedUsesTopicTimeIndex(t *testing.T) {
	pool, topic := newOutboxPool(t)
	ctx := context.Background()
	// Force the planner off sequential scans so the test proves the predicate
	// shape is indexable by idx_domain_events_topic_time (on a tiny test table
	// the planner would otherwise pick a seq scan regardless).
	conn, err := pool.Acquire(ctx)
	require.NoError(t, err)
	defer conn.Release()
	_, err = conn.Exec(ctx, `SET enable_seqscan = off`)
	require.NoError(t, err)
	defer func() { _, _ = conn.Exec(ctx, `RESET enable_seqscan`) }()

	rows, err := conn.Query(ctx, `EXPLAIN SELECT id FROM domain_events
		WHERE topic = ANY($1::text[]) AND occurred_at >= now() - interval '24 hours'
		  AND occurred_at < now() - interval '30 seconds'
		  AND dispatched_at IS NULL AND dispatch_attempts < 10
		ORDER BY occurred_at ASC LIMIT 100`, []string{topic, "order.paid"})
	require.NoError(t, err)
	var plan string
	for rows.Next() {
		var line string
		require.NoError(t, rows.Scan(&line))
		plan += line + "\n"
	}
	require.NoError(t, rows.Err())
	require.Contains(t, plan, "idx_domain_events_topic_time", plan)
}
