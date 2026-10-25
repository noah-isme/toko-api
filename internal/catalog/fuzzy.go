package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	dbgen "github.com/noah-isme/backend-toko/internal/db/gen"
)

// fuzzyWordSimilarityThreshold is the pg_trgm word_similarity threshold used by the
// fuzzy fallback (the default 0.6 rejects common typos such as "spatu" vs "sepatu").
// SET cannot bind parameters, so this constant is the only literal interpolated into SQL.
const fuzzyWordSimilarityThreshold = "0.4"

// FuzzyRunner runs fn against a QueryProvider whose session has the trigram
// threshold configured for the fuzzy (pg_trgm) search pass.
type FuzzyRunner interface {
	RunFuzzy(ctx context.Context, fn func(q QueryProvider) error) error
}

// TxBeginner is satisfied by *pgxpool.Pool and pgx.Conn.
type TxBeginner interface {
	BeginTx(ctx context.Context, opts pgx.TxOptions) (pgx.Tx, error)
}

// PoolFuzzyRunner executes the fuzzy pass inside a short read-only transaction
// that sets pg_trgm.word_similarity_threshold with SET LOCAL, so no pool-wide or
// database-wide state changes.
type PoolFuzzyRunner struct {
	Pool TxBeginner
}

// RunFuzzy implements FuzzyRunner.
func (r PoolFuzzyRunner) RunFuzzy(ctx context.Context, fn func(q QueryProvider) error) error {
	if r.Pool == nil {
		return fmt.Errorf("fuzzy runner: pool not configured")
	}
	tx, err := r.Pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("begin fuzzy tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL pg_trgm.word_similarity_threshold = "+fuzzyWordSimilarityThreshold); err != nil {
		return fmt.Errorf("set trigram threshold: %w", err)
	}
	if err := fn(dbgen.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit fuzzy tx: %w", err)
	}
	return nil
}
