# Backend Toko API

Backend service powering catalogue, checkout, and webhook flows for Toko.

## Observability & Performance
- **SLO**: public HTTP endpoints p95 < ${PERF_SLO_HTTP_P95_MS} ms and error rate < ${PERF_SLO_HTTP_ERROR_RATE}; webhook dispatch p99 < ${PERF_SLO_WEBHOOK_P99_MS} ms. See [`docs/ops/SLO.md`](docs/ops/SLO.md).
- **Prometheus alerts**: defined in [`deploy/prometheus/alerts.yml`](deploy/prometheus/alerts.yml) covering latency, error rate, HTTP saturation, Redis errors, and DB pool saturation. Tune thresholds via environment variables or by editing the rule file.
- **Grafana dashboards**: import JSON definitions from [`deploy/grafana/dashboards`](deploy/grafana/dashboards) (`overview`, `api`, `db_redis`, `webhook`). Each uses auto interval and descriptive legends.
- **Load tests**: scenarios under [`perf/k6`](perf/k6) with execution guidance in [`perf/README.md`](perf/README.md). CI smoke runs via the `perf-smoke` workflow and fails if latency or error budgets regress.

## Operations
- Database tuning indexes shipped in `migrations/0013_perf_indexes.up.sql`.
- Connection pool, statement cache, and concurrency guard configurable via environment variables (`DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME_MIN`, `DB_STATEMENT_CACHE_CAPACITY`, `HTTP_MAX_INFLIGHT`).
- Redis cache prefix & TTLs adjustable (`REDIS_CACHE_PREFIX`, `CATALOG_CACHE_TTL_SEC`, `ANALYTICS_CACHE_TTL_SEC`).
