# ADR-009: In-Process Circuit Breakers for Outbound Calls

**Status:** Accepted  
**Date:** 2024-05  

## Context

The API makes outbound HTTP calls to payment gateways (Midtrans, Xendit), shipping providers (RajaOngkir), and webhook endpoints. These calls can fail due to network issues, rate limits, or provider outages. Without protection, cascading failures can exhaust connection pools and degrade the entire service.

## Decision

Implement **in-process circuit breakers** (`internal/resilience`) wrapping all outbound HTTP calls:

- `resilience.Breaker` tracks failure rate over a sliding window.
- Opens the circuit when failure rate exceeds a configurable threshold, short-circuiting calls for a cool-down period.
- `resilience.HTTPClient` combines the breaker with jittered exponential back-off retries and per-request timeouts.
- Separate breaker instances for webhook delivery, payment, and shipping.

Configuration via environment:
- `CIRCUIT_*_MIN_REQ`, `CIRCUIT_*_FAILURE_RATE`, `CIRCUIT_*_OPEN_FOR`
- `RETRY_BASE`, `RETRY_MAX_ATTEMPTS`, `RETRY_JITTER_PERCENT`

## Consequences

- **Positive:** Failing providers are quickly isolated; healthy providers continue serving.
- **Positive:** Jittered retries prevent thundering-herd on recovery.
- **Positive:** Per-circuit Prometheus metrics (`breaker_state`, `breaker_trips_total`) enable observability.
- **Negative:** In-process state is not shared across replicas — each pod tracks failure independently.
- **Negative:** Tuning thresholds requires load testing and production observation.
