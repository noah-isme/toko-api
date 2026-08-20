# ADR-007: Embedded Migrations in Binary

**Status:** Accepted  
**Date:** 2024-04  

## Context

Running migrations in production typically requires either:
1. Shipping SQL files alongside the binary and using an external migration CLI.
2. Embedding SQL files into the binary at build time.

Option 1 introduces drift risk: the binary can be deployed with the wrong set of migration files if they're packaged separately.

## Decision

Use Go's `embed` directive to bake migration files into the `cmd/migrate` binary at compile time. The `Dockerfile` builds the migrate binary alongside the API and worker. A Kubernetes `Job` runs migrations before the API deployment rolls out.

```
cmd/migrate/
├── embed.go      # //go:embed migrations/*.sql
└── main.go       # golang-migrate/migrate runner
```

## Consequences

- **Positive:** The binary always carries exactly the schema its code expects — no version mismatch.
- **Positive:** No external CLI or migration files needed at runtime.
- **Positive:** Migration job is a Job (not an initContainer), preventing replicas from racing concurrent migrations.
- **Negative:** Adding a migration requires rebuilding the binary.
- **Negative:** Embedded SQL increases binary size slightly (~200 KB for 38 migrations).
