package txretry

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestRunRetriesDeadlock(t *testing.T) {
	calls := 0
	err := Run(context.Background(), 3, func(context.Context) error {
		calls++
		if calls < 3 {
			return fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: "40P01"})
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d, want nil and 3", err, calls)
	}
}

func TestRunStopsOnOtherErrors(t *testing.T) {
	calls := 0
	boom := errors.New("boom")
	err := Run(context.Background(), 3, func(context.Context) error {
		calls++
		return boom
	})
	if !errors.Is(err, boom) || calls != 1 {
		t.Fatalf("err=%v calls=%d, want boom and 1", err, calls)
	}
}

func TestRunGivesUpAfterAttempts(t *testing.T) {
	calls := 0
	err := Run(context.Background(), 2, func(context.Context) error {
		calls++
		return &pgconn.PgError{Code: "40001"}
	})
	if !IsRetryable(err) || calls != 2 {
		t.Fatalf("err=%v calls=%d, want retryable error and 2", err, calls)
	}
}
