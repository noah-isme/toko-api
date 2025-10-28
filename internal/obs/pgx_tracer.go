package obs

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type ctxSpanKey struct{}

// PGXTracer implements pgx.QueryTracer to create spans for database interactions.
type PGXTracer struct{}

// TraceQueryStart starts a span for the SQL statement.
func (PGXTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	ctx, span := otel.Tracer("db.pgx").Start(ctx, "pgx.query")
	span.SetAttributes(
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", truncateSQL(data.SQL)),
	)
	if strings.TrimSpace(data.SQL) != "" {
		span.SetAttributes(attribute.String("db.operation", strings.Fields(data.SQL)[0]))
	}
	return context.WithValue(ctx, ctxSpanKey{}, span)
}

// TraceQueryEnd ends the span and records any error.
func (PGXTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if span, ok := ctx.Value(ctxSpanKey{}).(trace.Span); ok {
		if data.Err != nil {
			span.RecordError(data.Err)
		}
		span.End()
	}
}

func truncateSQL(sql string) string {
	trimmed := strings.TrimSpace(sql)
	if len(trimmed) > 300 {
		return trimmed[:300] + "..."
	}
	return trimmed
}
