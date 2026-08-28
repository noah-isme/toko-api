# ADR-003: go-chi as HTTP Router

**Status:** Accepted
**Date:** 2024-01

## Context

We evaluated several Go routers: `net/http` (1.22 mux), `chi`, `gorilla/mux`, `echo`, and `gin`. Requirements: stdlib `http.Handler` compatibility, middleware chaining, route groups, and URL parameter extraction.

## Decision

Use **go-chi/chi v5**. It satisfies all requirements while staying close to `net/http` conventions. chi handlers are plain `http.HandlerFunc`, making them testable with `httptest` without framework lock-in.

## Consequences

- **Positive:** Middleware is composable via `chi.Use`, `chi.With`, and `chi.Group`.
- **Positive:** No custom `Context` type — uses `context.Context` from the stdlib.
- **Positive:** Route pattern extraction (`chi.URLParam`) is simple and well-tested.
- **Negative:** chi does not include request validation, response serialization, or OpenAPI generation — those are handled by hand or separate tools.
