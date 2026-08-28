# ADR-001: Modular Monolith Over Microservices

**Status:** Accepted
**Date:** 2024-01

## Context

At project inception we needed to choose between a microservices architecture and a monolithic one. The team is small (1–3 engineers), the domain boundaries were still being discovered, and operational overhead of running multiple services with inter-service auth, distributed tracing across network hops, and per-service CI pipelines was prohibitive.

## Decision

Build a **modular monolith**: keep the business domains in one Go codebase, isolated in their own `internal/<domain>` packages, while producing separate `api`, `worker`, and `migrate` binaries. Packages communicate through Go interfaces and in-process calls, not service-to-service network calls. The database is shared, while each domain owns its tables and queries.

Key rules:
- Each domain exposes a `Handler` (HTTP), a `Service` (business logic), and data-access via sqlc-generated `Queries`.
- Cross-domain calls go through exported interfaces, not internal types.
- No import cycles between domain packages.

## Consequences

- **Positive:** One codebase and shared deployment workflow, trivial local development, zero inter-service latency, and atomic database transactions across the checkout order/reservation writes.
- **Positive:** Domains can be extracted to services later where an explicit interface boundary exists, by replacing selected in-process calls with RPC.
- **Negative:** API domains share the same process and database connection pool; a misbehaving domain can affect other API routes.
- **Negative:** Schema migrations are global and require coordination.
- **Mitigated:** The `cmd/worker` binary runs background jobs in a separate process to isolate long-running tasks from the HTTP serving path.
