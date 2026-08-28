# ADR-002: sqlc for Data Access

**Status:** Accepted
**Date:** 2024-01

## Context

Go's `database/sql` requires hand-written row scanning. ORMs like GORM add implicit behaviour (lazy loading, auto-migrate) that's hard to reason about in production. We wanted compile-time–safe SQL without runtime reflection.

## Decision

Use **sqlc** to generate Go code from hand-written SQL in `internal/db/queries/`. The generated output lands in `internal/db/gen/` and is never edited by hand.

Workflow:
1. Write or modify `.sql` files in `internal/db/queries/`.
2. Run `make sqlc` to regenerate.
3. Commit the generated output.

## Consequences

- **Positive:** Queries are plain SQL — no DSL to learn, easy to optimise with `EXPLAIN`.
- **Positive:** Generated code is type-safe; running sqlc regeneration against the current schema catches query/schema incompatibilities before the generated code is committed.
- **Positive:** No runtime reflection; generated scanners are fast.
- **Negative:** Every schema change requires regeneration and a commit of generated files.
- **Negative:** Complex dynamic filters (e.g., optional WHERE clauses) require `sqlc.narg()` or manual query building.
