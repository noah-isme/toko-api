package queue

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrStoreUnavailable indicates the DLQ store dependency is not configured.
var ErrStoreUnavailable = errors.New("queue: store unavailable")

// Store provides database accessors for queue DLQ operations.
type Store interface {
	InsertQueueDlq(ctx context.Context, entry DLQEntry) (uuid.UUID, error)
	DeleteQueueDlq(ctx context.Context, id uuid.UUID) error
	GetQueueDlq(ctx context.Context, id uuid.UUID) (DLQEntry, error)
	ListQueueDlq(ctx context.Context, kind string, limit, offset int) ([]DLQEntry, error)
	CountQueueDlq(ctx context.Context, kind string) (int64, error)
	QueueDlqSizeByKind(ctx context.Context) (map[string]int64, error)
}

// DLQEntry represents an item stored in the DLQ table.
type DLQEntry struct {
	ID             uuid.UUID
	Kind           string
	IdempotencyKey string
	Payload        []byte
	Attempts       int
	LastError      *string
	CreatedAt      time.Time
}

// NewStore constructs a Store backed by a pgx connection pool.
func NewStore(pool *pgxpool.Pool) Store {
	return &pgStore{pool: pool}
}

type pgStore struct {
	pool *pgxpool.Pool
}

// InsertQueueDlq persists a DLQ entry and returns the generated identifier.
func (s *pgStore) InsertQueueDlq(ctx context.Context, entry DLQEntry) (uuid.UUID, error) {
	if s == nil || s.pool == nil {
		return uuid.Nil, ErrStoreUnavailable
	}
	var lastError any
	if entry.LastError != nil {
		lastError = *entry.LastError
	}
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `INSERT INTO queue_dlq (kind, idem_key, payload, attempts, last_error)
VALUES ($1, $2, $3, $4, $5) RETURNING id`, entry.Kind, entry.IdempotencyKey, entry.Payload, entry.Attempts, lastError).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

// DeleteQueueDlq removes a DLQ entry by ID.
func (s *pgStore) DeleteQueueDlq(ctx context.Context, id uuid.UUID) error {
	if s == nil || s.pool == nil {
		return ErrStoreUnavailable
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM queue_dlq WHERE id = $1`, id)
	return err
}

// GetQueueDlq fetches a DLQ entry by ID.
func (s *pgStore) GetQueueDlq(ctx context.Context, id uuid.UUID) (DLQEntry, error) {
	if s == nil || s.pool == nil {
		return DLQEntry{}, ErrStoreUnavailable
	}
	row := s.pool.QueryRow(ctx, `SELECT id, kind, idem_key, payload, attempts, last_error, created_at FROM queue_dlq WHERE id = $1`, id)
	var entry DLQEntry
	var lastErr sql.NullString
	if err := row.Scan(&entry.ID, &entry.Kind, &entry.IdempotencyKey, &entry.Payload, &entry.Attempts, &lastErr, &entry.CreatedAt); err != nil {
		return DLQEntry{}, err
	}
	if lastErr.Valid {
		entry.LastError = &lastErr.String
	}
	return entry, nil
}

// ListQueueDlq fetches DLQ entries filtered by kind with pagination.
func (s *pgStore) ListQueueDlq(ctx context.Context, kind string, limit, offset int) ([]DLQEntry, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	limit = clampPositive(limit, 1, 500)
	if offset < 0 {
		offset = 0
	}
	kind = strings.TrimSpace(kind)
	var (
		rows pgx.Rows
		err  error
	)
	if kind != "" {
		rows, err = s.pool.Query(ctx, `SELECT id, kind, idem_key, payload, attempts, last_error, created_at FROM queue_dlq WHERE kind = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`, kind, limit, offset)
	} else {
		rows, err = s.pool.Query(ctx, `SELECT id, kind, idem_key, payload, attempts, last_error, created_at FROM queue_dlq ORDER BY created_at DESC LIMIT $1 OFFSET $2`, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	entries := make([]DLQEntry, 0, limit)
	for rows.Next() {
		var entry DLQEntry
		var lastErr sql.NullString
		if err := rows.Scan(&entry.ID, &entry.Kind, &entry.IdempotencyKey, &entry.Payload, &entry.Attempts, &lastErr, &entry.CreatedAt); err != nil {
			return nil, err
		}
		if lastErr.Valid {
			entry.LastError = &lastErr.String
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

// CountQueueDlq counts DLQ items optionally filtered by kind.
func (s *pgStore) CountQueueDlq(ctx context.Context, kind string) (int64, error) {
	if s == nil || s.pool == nil {
		return 0, ErrStoreUnavailable
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		var total int64
		if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM queue_dlq`).Scan(&total); err != nil {
			return 0, err
		}
		return total, nil
	}
	var total int64
	if err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM queue_dlq WHERE kind = $1`, kind).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

// QueueDlqSizeByKind returns aggregated DLQ sizes per kind.
func (s *pgStore) QueueDlqSizeByKind(ctx context.Context) (map[string]int64, error) {
	if s == nil || s.pool == nil {
		return nil, ErrStoreUnavailable
	}
	rows, err := s.pool.Query(ctx, `SELECT kind, COUNT(*) FROM queue_dlq GROUP BY kind`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]int64)
	for rows.Next() {
		var (
			kind  string
			total int64
		)
		if err := rows.Scan(&kind, &total); err != nil {
			return nil, err
		}
		result[kind] = total
	}
	return result, rows.Err()
}

func clampPositive(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
