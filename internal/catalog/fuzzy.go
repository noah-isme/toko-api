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

// DefaultFuzzyStatementTimeout bounds each statement of the fuzzy pass (count, list,
// facets and suggestions). These are anonymous, trigram-based reads whose cost grows
// with the catalog and with len(q). ListProducts degrades to the FTS result when it fires.
const DefaultFuzzyStatementTimeout = 500 * time.Millisecond

// DefaultFuzzyPassTimeout bounds the whole fuzzy pass (begin, up to six statements,
// commit). statement_timeout alone would let one request hold a pool connection for
// six times DefaultFuzzyStatementTimeout; this deadline caps it.
const DefaultFuzzyPassTimeout = time.Second

// ErrFuzzyTimeout wraps the error returned by RunFuzzy when the pass hit its statement
// timeout or its pass deadline while the caller's context was still live. Callers
// degrade on it; a cancelled caller context is reported as a plain error instead.
var ErrFuzzyTimeout = errors.New("fuzzy search timed out")

// pgQueryCanceled is SQLSTATE 57014 (query_canceled), raised when statement_timeout fires.
const pgQueryCanceled = "57014"

// FuzzyRunner runs fn against a QueryProvider whose session has the trigram
// threshold configured for the fuzzy (pg_trgm) search pass and a short statement timeout.
// fn must issue its queries with the context it is given, which carries the pass deadline.
type FuzzyRunner interface {
	RunFuzzy(ctx context.Context, fn func(ctx context.Context, q QueryProvider) error) error
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
	// PassTimeout overrides DefaultFuzzyPassTimeout when positive.
	PassTimeout time.Duration
}

// RunFuzzy implements FuzzyRunner. The transaction and fn run under a child context
// with the pass deadline; expiry of that deadline or of statement_timeout is reported as
// ErrFuzzyTimeout only while ctx itself is still live.
func (r PoolFuzzyRunner) RunFuzzy(ctx context.Context, fn func(ctx context.Context, q QueryProvider) error) (err error) {
	if r.Pool == nil {
		return fmt.Errorf("fuzzy runner: pool not configured")
	}
	timeout := r.StatementTimeout
	if timeout <= 0 {
		timeout = DefaultFuzzyStatementTimeout
	}
	pass := r.PassTimeout
	if pass <= 0 {
		pass = DefaultFuzzyPassTimeout
	}
	passCtx, cancel := context.WithTimeout(ctx, pass)
	defer cancel()
	defer func() {
		if err != nil && ctx.Err() == nil && (passCtx.Err() != nil || isStatementTimeout(err)) {
			err = fmt.Errorf("%w: %w", ErrFuzzyTimeout, err)
		}
	}()
	tx, err := r.Pool.BeginTx(passCtx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin fuzzy tx: %w", err)
	}
	// Rollback uses the caller's context so it still runs after the pass deadline.
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(passCtx,
		"SELECT set_config('statement_timeout', $1, true), set_config('pg_trgm.word_similarity_threshold', $2, true)",
		statementTimeoutSetting(timeout), formatThreshold(fuzzyWordSimilarityThreshold),
	); err != nil {
		return fmt.Errorf("configure fuzzy tx: %w", err)
	}
	if err := fn(passCtx, dbgen.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(passCtx); err != nil {
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
