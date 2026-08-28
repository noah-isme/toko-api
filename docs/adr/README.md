# Architecture Decision Records

This directory contains Architecture Decision Records (ADRs) for the Toko API.

## Index

| # | Title | Status | Date |
|---|-------|--------|------|
| [001](001-monolith-modular-layout.md) | Modular monolith over microservices | Accepted | 2024-01 |
| [002](002-sqlc-codegen.md) | sqlc for data access | Accepted | 2024-01 |
| [003](003-chi-router.md) | go-chi as HTTP router | Accepted | 2024-01 |
| [004](004-jwt-auth.md) | JWT authentication with refresh tokens | Accepted | 2024-02 |
| [005](005-multi-tenancy.md) | Header-based multi-tenancy with membership guard | Accepted | 2024-06 |
| [006](006-payment-provider-interface.md) | Pluggable payment provider interface | Accepted | 2024-03 |
| [007](007-embedded-migrations.md) | Embedded migrations in binary | Accepted | 2024-04 |
| [008](008-redis-task-queue.md) | Redis-based task queue over external broker | Accepted | 2024-05 |
| [009](009-circuit-breaker-resilience.md) | In-process circuit breaker for webhook delivery | Accepted | 2024-05 |
| [010](010-observability-stack.md) | Prometheus + OpenTelemetry + zerolog | Accepted | 2024-03 |

## Format

Each ADR follows this template:

```
# ADR-NNN: Title

**Status:** Proposed | Accepted | Deprecated | Superseded
**Date:** YYYY-MM

## Context
## Decision
## Consequences
```
