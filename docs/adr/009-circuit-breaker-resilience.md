# ADR-009: In-Process Circuit Breaker for Webhook Delivery

**Status:** Accepted
**Date:** 2024-05

## Context

The API makes outbound HTTP calls to payment gateways (Midtrans, Xendit), shipping providers (RajaOngkir), and webhook endpoints. These calls can fail due to network issues, rate limits, or provider outages. The resilience package provides reusable protection primitives, but the current wiring must be documented separately from the intended broader scope.

## Decision

Implement **in-process circuit-breaker and retry primitives** in `internal/resilience`. The current production wiring wraps webhook delivery in `resilience.HTTPClient` from both `cmd/api` and `cmd/worker`:

- `resilience.Breaker` tracks successes and failures with bounded rolling counters.
- Opens the circuit when the failure ratio reaches a configurable threshold, short-circuiting calls for a cool-down period.
- `resilience.HTTPClient` combines the breaker with jittered exponential back-off retries and per-request timeouts.
- The configured breaker target is `webhook-delivery`. Payment and shipping provider clients currently use their own plain `http.Client` instances and are not wrapped by this package.

Configuration via environment:
- Webhook breaker thresholds: `CB_WEBHOOK_MIN_REQUESTS` (default 5), `CB_WEBHOOK_FAILURE_RATE_THRESHOLD` (default 0.5), and `CB_WEBHOOK_OPEN_SEC` (default 30).
- Shared HTTP retry/timeout settings: `RETRY_BASE_MS` (default 200), `RETRY_MAX_ATTEMPTS` (default 5), `RETRY_JITTER_PCT` (default 0.2), and `OUTBOUND_TIMEOUT_MS` (default 5000). The underlying webhook client also uses `WEBHOOK_REQUEST_TIMEOUT_MS` (default 5000).

## Consequences

- **Positive:** Failing providers are quickly isolated; healthy providers continue serving.
- **Positive:** Jittered retries prevent thundering-herd on recovery.
- **Positive:** Prometheus metrics (`breaker_state{target}`, `breaker_transition_total{target,from,to}`, and `breaker_open_total{target}`) enable observability when the `/metrics` endpoint is enabled.
- **Negative:** In-process state is not shared across replicas — each pod tracks failure independently.
- **Negative:** Tuning thresholds requires load testing and production observation.
