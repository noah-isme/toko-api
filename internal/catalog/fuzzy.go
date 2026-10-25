package catalog

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// fuzzyWordSimilarityThreshold is the pg_trgm word_similarity threshold used by the
// fuzzy fallback (the default 0.6 rejects common typos such as "spatu" vs "sepatu").
const fuzzyWordSimilarityThreshold = 0.4

// DefaultFuzzyStatementTimeout bounds every statement of the fuzzy pass (count, list,
// facets and suggestions). These are anonymous, trigram-based reads whose cost grows
// with the catalog and with len(q); the timeout keeps one client from pinning pool
// connections. ListProducts degrades to the FTS result when it fires.
const DefaultFuzzyStatementTimeout = 500 * time.Millisecond

// pgQueryCanceled is SQLSTATE 57014 (query_canceled), raised when statement_timeout fires.
const pgQueryCanceled = "57014"

// FuzzyRunner runs fn against a QueryProvider whose session has the trigram
// threshold configured for the fuzzy (pg_trgm) search pass and a short statement timeout.
type FuzzyRunner interface {
	RunFuzzy(ctx context.Context, fn func(q QueryProvider) error) error
}

// TxBeginner is satisfied by *pgxpool.Pool and pgx.Conn.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// PoolFuzzyRunner executes the fuzzy pass inside a short read-only transaction that
// sets pg_trgm.word_similarity_threshold and statement_timeout with set_config(...,
// is_local => true) (the parameterised form of SET LOCAL), so no pool-wide or
// database-wide state changes.
type PoolFuzzyRunner struct {
	Pool TxBeginner
	// StatementTimeout overrides DefaultFuzzyStatementTimeout when positive.
	StatementTimeout time.Duration
}

// RunFuzzy implements FuzzyRunner.
func (r PoolFuzzyRunner) RunFuzzy(ctx context.Context, fn func(q QueryProvider) error) error {
	if r.Pool == nil {
		return fmt.Errorf("fuzzy runner: pool not configured")
	}
	timeout := r.StatementTimeout
	if timeout <= 0 {
		timeout = DefaultFuzzyStatementTimeout
	}
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin fuzzy tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		"SELECT set_config('statement_timeout', $1, true), set_config('pg_trgm.word_similarity_threshold', $2, true)",
		statementTimeoutSetting(timeout), formatThreshold(fuzzyWordSimilarityThreshold),
	); err != nil {
		return fmt.Errorf("configure fuzzy tx: %w", err)
	}
	if err := fn(dbgen.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit fuzzy tx: %w", err)
	}
	return nil
}

// statementTimeoutSetting renders d as a statement_timeout value in whole milliseconds (min 1ms).
func statementTimeoutSetting(d time.Duration) string {
	ms := d.Milliseconds()
	if ms < 1 {
		ms = 1
	}
	return strconv.FormatInt(ms, 10) + "ms"
}

func formatThreshold(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// isStatementTimeout reports whether err is a server-side statement cancellation.
func isStatementTimeout(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == pgQueryCanceled
}
