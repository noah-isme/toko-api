# ADR-001: Modular Monolith Over Microservices

**Status:** Accepted  
**Date:** 2024-01  

## Context

At project inception we needed to choose between a microservices architecture and a monolithic one. The team is small (1–3 engineers), the domain boundaries were still being discovered, and operational overhead of running multiple services with inter-service auth, distributed tracing across network hops, and per-service CI pipelines was prohibitive.

## Decision

Build a **modular monolith**: a single Go binary (`cmd/api`) with each business domain isolated in its own `internal/<domain>` package. Packages communicate through Go interfaces, not network calls. The database is shared but each domain owns its tables and queries.

Key rules:
- Each domain exposes a `Handler` (HTTP), a `Service` (business logic), and data-access via sqlc-generated `Queries`.
- Cross-domain calls go through exported interfaces, not internal types.
- No import cycles between domain packages.

## Consequences

- **Positive:** Single deployment unit, trivial local development, zero inter-service latency, atomic database transactions across domains (e.g., checkout).
- **Positive:** Domains can be extracted to services later by replacing in-process calls with RPC — interfaces are already defined.
- **Negative:** All domains share the same process and database connection pool; a misbehaving domain can affect others.
- **Negative:** Schema migrations are global and require coordination.
- **Mitigated:** The `cmd/worker` binary runs background jobs in a separate process to isolate long-running tasks from the HTTP serving path.
