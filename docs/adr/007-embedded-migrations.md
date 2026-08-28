# ADR-007: Embedded Migrations in Binary

**Status:** Accepted
**Date:** 2024-04

## Context

Running migrations in production typically requires either:
1. Shipping SQL files alongside the binary and using an external migration CLI.
2. Embedding SQL files into the binary at build time.

Option 1 introduces drift risk: the binary can be deployed with the wrong set of migration files if they're packaged separately.

## Decision

Use Go's `embed` directive in the repository's root `embed.go` to bake migration files into the `cmd/migrate` binary at compile time. The `Dockerfile` builds the migrate binary alongside the API and worker. The Kubernetes manifests provide a Job that runs migrations before the API deployment rolls out; the active production workflow instead runs the uploaded `migrate` binary over SSH before restarting systemd services.

```
embed.go          # //go:embed migrations/*.sql
cmd/migrate/
└── main.go       # golang-migrate/migrate runner
```

## Consequences

- **Positive:** Each built migration binary carries the exact migration set from its source commit, avoiding a separately packaged SQL directory.
- **Positive:** No external CLI or migration files needed at runtime.
- **Positive:** Migration job is a Job (not an initContainer), preventing replicas from racing concurrent migrations.
- **Negative:** Adding a migration requires rebuilding the binary.
- **Negative:** Embedded SQL increases the migration binary size as migrations are added.
