package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func TestStatementTimeoutSetting(t *testing.T) {
	require.Equal(t, "500ms", statementTimeoutSetting(DefaultFuzzyStatementTimeout))
	require.Equal(t, "1500ms", statementTimeoutSetting(1500*time.Millisecond))
	require.Equal(t, "1ms", statementTimeoutSetting(10*time.Microsecond), "never 0, which would disable the timeout")
}

func TestFormatThreshold(t *testing.T) {
	require.Equal(t, "0.4", formatThreshold(fuzzyWordSimilarityThreshold))
	require.Equal(t, "0.2", formatThreshold(suggestionMinScore))
}

func TestIsStatementTimeout(t *testing.T) {
	timeout := &pgconn.PgError{Code: "57014"}
	require.True(t, isStatementTimeout(timeout))
	require.True(t, isStatementTimeout(fmt.Errorf("count products: %w", timeout)))
	require.False(t, isStatementTimeout(&pgconn.PgError{Code: "42P01"}))
	require.False(t, isStatementTimeout(errors.New("boom")))
	require.False(t, isStatementTimeout(nil))
}

func TestFuzzyEligible(t *testing.T) {
	require.False(t, fuzzyEligible(""))
	require.False(t, fuzzyEligible("ab"))
	require.True(t, fuzzyEligible("abc"))
	require.True(t, fuzzyEligible(strings.Repeat("é", maxFuzzyQueryRunes)))
	require.False(t, fuzzyEligible(strings.Repeat("a", maxFuzzyQueryRunes+1)))
}

// capturingBeginner exposes the fuzzy transaction so the test can inspect its settings.
type capturingBeginner struct {
	pool *pgxpool.Pool
	tx   *pgx.Tx
}

func (c capturingBeginner) BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error) {
	tx, err := c.pool.BeginTx(ctx, opts)
	*c.tx = tx
	return tx, err
}

// TestIntegrationPoolFuzzyRunnerLimits needs TEST_DATABASE_URL (migrated database).
func TestIntegrationPoolFuzzyRunnerLimits(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping Postgres integration test")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(url)
	require.NoError(t, err)
	cfg.MaxConns = 1 // the post-commit check must see the same connection
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	var tx pgx.Tx
	runner := PoolFuzzyRunner{Pool: capturingBeginner{pool: pool, tx: &tx}}
	require.NoError(t, runner.RunFuzzy(ctx, func(ctx context.Context, q QueryProvider) error {
		_, hasDeadline := ctx.Deadline()
		require.True(t, hasDeadline, "fn runs under the pass deadline")
		var timeout, threshold string
		require.NoError(t, tx.QueryRow(ctx,
			"SELECT current_setting('statement_timeout'), current_setting('pg_trgm.word_similarity_threshold')").Scan(&timeout, &threshold))
		require.Equal(t, "500ms", timeout)
		require.Equal(t, "0.4", threshold)
		// The suggestion step lowers the threshold inside the same transaction.
		require.NoError(t, q.SetLocalWordSimilarityThreshold(ctx, formatThreshold(suggestionMinScore)))
		require.NoError(t, tx.QueryRow(ctx, "SELECT current_setting('pg_trgm.word_similarity_threshold')").Scan(&threshold))
		require.Equal(t, "0.2", threshold)
		return nil
	}))
	// Transaction-local: the pooled connection is back to the server default afterwards.
	var after string
	require.NoError(t, pool.QueryRow(ctx, "SHOW statement_timeout").Scan(&after))
	require.Equal(t, "0", after)

	// The transaction is read-only.
	err = runner.RunFuzzy(ctx, func(ctx context.Context, _ QueryProvider) error {
		_, err := tx.Exec(ctx, "CREATE TEMP TABLE fuzzy_ro_probe (i int)")
		return err
	})
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	require.Equal(t, "25006", pgErr.Code) // read_only_sql_transaction
	require.NotErrorIs(t, err, ErrFuzzyTimeout)

	// A statement over the limit is cancelled server-side with 57014.
	short := PoolFuzzyRunner{Pool: capturingBeginner{pool: pool, tx: &tx}, StatementTimeout: 50 * time.Millisecond}
	started := time.Now()
	err = short.RunFuzzy(ctx, func(ctx context.Context, _ QueryProvider) error {
		_, err := tx.Exec(ctx, "SELECT pg_sleep(2)")
		return err
	})
	require.True(t, isStatementTimeout(err), "got %v", err)
	require.ErrorIs(t, err, ErrFuzzyTimeout)
	require.Less(t, time.Since(started), time.Second)

	// The pass deadline caps the whole pass even when every statement stays under
	// statement_timeout.
	bounded := PoolFuzzyRunner{Pool: capturingBeginner{pool: pool, tx: &tx}, StatementTimeout: 2 * time.Second, PassTimeout: 150 * time.Millisecond}
	started = time.Now()
	statements := 0
	err = bounded.RunFuzzy(ctx, func(ctx context.Context, _ QueryProvider) error {
		for i := 0; i < 10; i++ {
			if _, err := tx.Exec(ctx, "SELECT pg_sleep(0.06)"); err != nil {
				return err
			}
			statements++
		}
		return nil
	})
	require.ErrorIs(t, err, ErrFuzzyTimeout, "got %v", err)
	require.Less(t, statements, 10)
	require.Less(t, time.Since(started), time.Second)

	// A caller that goes away is not a timeout: the error must not be degraded.
	pool.Reset() // the deadline above may have closed the only connection
	parent, cancelParent := context.WithCancel(ctx)
	time.AfterFunc(50*time.Millisecond, cancelParent)
	err = bounded.RunFuzzy(parent, func(ctx context.Context, _ QueryProvider) error {
		_, err := tx.Exec(ctx, "SELECT pg_sleep(1)")
		return err
	})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrFuzzyTimeout)

	// The pool still serves queries afterwards.
	pool.Reset()
	var one int
	require.NoError(t, pool.QueryRow(context.Background(), "SELECT 1").Scan(&one))
}
