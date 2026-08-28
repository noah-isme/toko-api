# ADR-010: Prometheus + OpenTelemetry + zerolog

**Status:** Accepted
**Date:** 2024-03

## Context

Observability requires three pillars: metrics, traces, and logs. We needed a stack that integrates with Kubernetes, is industry-standard, and has low overhead.

## Decision

- **Metrics:** Prometheus client library (`prometheus/client_golang`). With the default `OBS_METRICS_NAMESPACE=toko`, HTTP metrics include `toko_http_request_duration_ms`, `toko_http_requests_total`, and `toko_http_in_flight_requests`, alongside domain, queue, and breaker collectors. They are exposed on `/metrics` when `OBS_ENABLE_PROMETHEUS` is enabled.
- **Tracing:** OpenTelemetry SDK with an OTLP/HTTP exporter. When `OBS_ENABLE_TRACING` is enabled and initialisation succeeds, `obs.TracingMiddleware` creates spans for incoming HTTP requests and `obs.PGXTracer` traces database queries. `OBS_TRACING_SAMPLING_RATIO` controls sampling (default 1.0).
- **Logging:** zerolog for structured logs (JSON by default; console/text is configurable). `obs.RequestLogger` middleware logs method, path, status, duration, and request ID on every response. Log level and format are configurable.

Grafana dashboard JSON definitions (`deploy/grafana/dashboards/`) provide views for HTTP, database, Redis, webhooks, and queue/breaker metrics; importing and wiring them to a Prometheus datasource is an operator step.

Prometheus alert rule files (`deploy/prometheus/alerts.yml` and `deploy/prometheus/alerts_queue_breaker.yml`) provide SLO and queue/breaker rules; loading them into a Prometheus installation is an operator step.

## Consequences

- **Positive:** All three pillars are covered with industry-standard, well-supported tools.
- **Positive:** Request logs include request and trace identifiers, while OpenTelemetry propagates trace context across instrumented calls; systems can correlate records when those identifiers are retained.
- **Positive:** Dashboard JSON and alert rule files ship with the repo, providing repeatable configuration for operators.
- **Negative:** Prometheus requires a scrape target; OTLP requires a collector endpoint.
- **Negative:** High cardinality labels (e.g., per-route metrics) need care to avoid metric explosion.
