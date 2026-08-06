# SLO Backend Toko

## Target SLO
- HTTP public endpoints: p95 latency < 250 ms (rolling 5m window) and error rate < 0.01 (1%).
- Webhook dispatch: p99 latency < 1500 ms.

## SLI & Metrics
- `toko_http_request_duration_ms`, `toko_http_requests_total` (labelled by method, route, status).
- `toko_http_in_flight_requests` to monitor saturation.
- `webhook_attempt_duration_ms`, `webhook_deliveries_total` for delivery health.
- `db_pool_in_use_ratio`, `db_pool_acquired_conns`, `db_pool_idle_conns` for Postgres saturation.
- `redis_client_errors_total` (from redisotel) for Redis health.

## Review & Escalation
- Breach of either SLO for >15m -> incident Sev2; owner: Backend Oncall.
- Create Jira ticket for follow-up postmortem within 48h of sustained breach.
- Document mitigations and tuning changes in `/docs/ops/QUERY_TUNING.md` and relevant service runbooks.

## Configuration
- `PERF_SLO_HTTP_P95_MS`: latency budget for public HTTP endpoints in milliseconds (default: 250).
- `PERF_SLO_HTTP_ERROR_RATE`: acceptable error ratio for public HTTP endpoints (0-1) (default: 0.01).
- `PERF_SLO_WEBHOOK_P99_MS`: latency budget for webhook deliveries in milliseconds (default: 1500).

## Alert Thresholds (from deploy/prometheus/alerts.yml)
- HighErrorRate (warning): 5xx rate > 1% for 5m
- HighErrorRate (critical): 5xx rate > 3% for 3m
- HighLatency (warning): p95 > 250ms for 5m
- HighLatency (critical): p95 > 350ms for 3m
- WebhookLatency (critical): p99 > 1500ms for 10m
- Saturation (warning): in-flight > 320 for 2m
- Saturation (critical): in-flight > 400 for 1m
