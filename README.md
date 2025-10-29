# Backend Toko API

Backend service powering catalogue, checkout, and webhook flows for Toko.

## Observability & Performance
- **SLO**: public HTTP endpoints p95 < ${PERF_SLO_HTTP_P95_MS} ms and error rate < ${PERF_SLO_HTTP_ERROR_RATE}; webhook dispatch p99 < ${PERF_SLO_WEBHOOK_P99_MS} ms. See [`docs/ops/SLO.md`](docs/ops/SLO.md).
- **Prometheus alerts**: defined in [`deploy/prometheus/alerts.yml`](deploy/prometheus/alerts.yml) covering latency, error rate, HTTP saturation, Redis errors, and DB pool saturation. Tune thresholds via environment variables or by editing the rule file.
- **Grafana dashboards**: import JSON definitions from [`deploy/grafana/dashboards`](deploy/grafana/dashboards) (`overview`, `api`, `db_redis`, `webhook`). Each uses auto interval and descriptive legends.
- **Load tests**: scenarios under [`perf/k6`](perf/k6) with execution guidance in [`perf/README.md`](perf/README.md). CI smoke runs via the `perf-smoke` workflow and fails if latency or error budgets regress.

## Operability
- Queue & breaker metrics diekspos melalui dashboard [`queue_breaker.json`](deploy/grafana/dashboards/queue_breaker.json) dan alert Prometheus [`alerts_queue_breaker.yml`](deploy/prometheus/alerts_queue_breaker.yml).
- Admin DLQ endpoints tersedia di `/api/v1/admin/queue/*` untuk list, replay, dan stats antrean.

## Operations
- Database tuning indexes shipped in `migrations/0013_perf_indexes.up.sql`.
- Connection pool, statement cache, and concurrency guard configurable via environment variables (`DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME_MIN`, `DB_STATEMENT_CACHE_CAPACITY`, `HTTP_MAX_INFLIGHT`).
- Redis cache prefix & TTLs adjustable (`REDIS_CACHE_PREFIX`, `CATALOG_CACHE_TTL_SEC`, `ANALYTICS_CACHE_TTL_SEC`).

## Scalability & Resilience
- Outbound Payment, Shipping, and Webhook clients run through circuit breakers with jittered retries and request timeouts.
- Background workers run in `cmd/worker` for webhook, email, and analytics tasks; the API only publishes jobs.
- Redis-backed distributed locks guard idempotent delivery and settlement replay flows.
- Graceful shutdown toggles readiness and drains inflight HTTP requests and queue jobs.
- Chaos playbooks live under `perf/chaos` to rehearse provider, Redis, and DB failure scenarios.
