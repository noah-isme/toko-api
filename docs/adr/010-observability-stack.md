# ADR-010: Prometheus + OpenTelemetry + zerolog

**Status:** Accepted  
**Date:** 2024-03  

## Context

Observability requires three pillars: metrics, traces, and logs. We needed a stack that integrates with Kubernetes, is industry-standard, and has low overhead.

## Decision

- **Metrics:** Prometheus client library (`prometheus/client_golang`). Custom histograms for HTTP latency (`toko_http_request_duration_ms`) and counters for requests, errors, and domain events. Exposed on `/metrics`.
- **Tracing:** OpenTelemetry SDK with OTLP exporter. `obs.TracingMiddleware` creates spans for every HTTP request; `obs.PGXTracer` traces database queries. Configurable sampling ratio.
- **Logging:** zerolog for structured JSON logs. `obs.RequestLogger` middleware logs method, path, status, duration, and request ID on every response. Log level and format are configurable.

Grafana dashboards (`deploy/grafana/dashboards/`) provide pre-built views for HTTP, database, Redis, and webhooks.

Prometheus alert rules (`deploy/prometheus/alerts.yml`) fire on SLO breaches.

## Consequences

- **Positive:** All three pillars are covered with industry-standard, well-supported tools.
- **Positive:** Metrics, traces, and logs correlate via request ID.
- **Positive:** Grafana dashboards and alerts ship with the repo — no manual setup.
- **Negative:** Prometheus requires a scrape target; OTLP requires a collector endpoint.
- **Negative:** High cardinality labels (e.g., per-route metrics) need care to avoid metric explosion.
