// Package txretry retries short database transactions that PostgreSQL aborted
// to resolve a deadlock or a serialization conflict.
package txretry

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// DefaultAttempts is the number of times Run executes fn before giving up.
const DefaultAttempts = 3

// IsRetryable reports whether err is a deadlock (40P01) or serialization
// failure (40001). PostgreSQL rolls the victim transaction back, so the whole
// transaction can be run again.
func IsRetryable(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40P01" || pgErr.Code == "40001"
	}
	return false
}

// Run calls fn until it succeeds, returns a non-retryable error, the context
// ends, or attempts are used up. fn must open and finish its own transaction
// so every attempt starts clean.
func Run(ctx context.Context, attempts int, fn func(ctx context.Context) error) error {
	if attempts <= 0 {
		attempts = DefaultAttempts
	}
	var err error
	for attempt := 1; attempt <= attempts; attempt++ {
		err = fn(ctx)
		if err == nil || !IsRetryable(err) || attempt == attempts {
			return err
		}
		backoff := time.Duration(attempt*attempt) * 20 * time.Millisecond
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return errors.Join(err, ctx.Err())
		case <-timer.C:
		}
	}
	return err
}
