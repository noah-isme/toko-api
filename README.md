# Backend Toko API

Backend service powering catalogue, checkout, and webhook flows for Toko.

## 📚 Documentation

- **[API Contract](docs/API_CONTRACT.md)** - Complete API documentation for frontend integration
- **[Quick Start for Frontend](docs/QUICK_START_FRONTEND.md)** - Quick guide for frontend developers (Bahasa Indonesia)
- **[TypeScript Types](docs/types.ts)** - TypeScript type definitions for API contracts
- **[Operations SLO](docs/ops/SLO.md)** - Service Level Objectives and performance targets
- **[Runbook](docs/ops/RUNBOOK.md)** - Operations and incident response guide

## Observability & Performance
- **SLO**: public HTTP endpoints p95 < 250 ms and error rate < 0.01; webhook dispatch p99 < 1500 ms. See [`docs/ops/SLO.md`](docs/ops/SLO.md).
- **Prometheus alerts**: defined in [`deploy/prometheus/alerts.yml`](deploy/prometheus/alerts.yml) covering latency, error rate, HTTP saturation, Redis errors, and DB pool saturation. Tune thresholds via environment variables or by editing the rule file.
- **Grafana dashboards**: import JSON definitions from [`deploy/grafana/dashboards`](deploy/grafana/dashboards) (`overview`, `api`, `db_redis`, `webhook`). Each uses auto interval and descriptive legends.
- **Load tests**: scenarios under [`perf/k6`](perf/k6) with execution guidance in [`perf/README.md`](perf/README.md). CI smoke runs via the `perf-smoke` workflow and fails if latency or error budgets regress.

## Operability
- Queue & breaker metrics diekspos melalui dashboard [`queue_breaker.json`](deploy/grafana/dashboards/queue_breaker.json) dan alert Prometheus [`alerts_queue_breaker.yml`](deploy/prometheus/alerts_queue_breaker.yml).
- Admin DLQ endpoints tersedia di `/api/v1/admin/queue/*` untuk list, replay, dan stats antrean.

## Operations
- Database tuning indexes shipped in `migrations/000015_perf_indexes.up.sql`.
- Connection pool, statement cache, and concurrency guard configurable via environment variables (`DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME_MIN`, `DB_STATEMENT_CACHE_CAPACITY`, `HTTP_MAX_INFLIGHT`).
- Redis cache prefix & TTLs adjustable (`REDIS_CACHE_PREFIX`, `CATALOG_CACHE_TTL_SEC`, `ANALYTICS_CACHE_TTL_SEC`).

## Scalability & Resilience
- Outbound Payment, Shipping, and Webhook clients run through circuit breakers with jittered retries and request timeouts.
- Background workers run in `cmd/worker` for webhook, email, and analytics tasks; the API only publishes jobs.
- Redis-backed distributed locks guard idempotent delivery and settlement replay flows.
- Graceful shutdown toggles readiness and drains inflight HTTP requests and queue jobs.
- Chaos playbooks live under `perf/chaos` to rehearse provider, Redis, and DB failure scenarios.

## 🛠 Development

### Prerequisites
- Go 1.22+
- Docker & Docker Compose
- [Air](https://github.com/cosmtrek/air) (for live reload)

### Local Development
1. Start infrastructure: `docker-compose up -d`
2. Run with live reload:
   ```bash
   make dev
   ```
   Or run directly with air:
   ```bash
   air
   ```

### Database Migrations
Migrations are embedded into a dedicated binary (`cmd/migrate`, see `embed.go`),
so no external CLI is required and a built image always carries exactly the
schema its code expects.

```bash
export DATABASE_URL=postgres://postgres:postgres@localhost:5432/toko?sslmode=disable
make migrate-up        # apply everything pending
make migrate-version   # show the current version
make migrate-down      # roll back one migration
```

A dirty schema (a previous run that failed partway) exits non-zero rather than
continuing; resolve it manually, then `go run ./cmd/migrate force <version>`.

The current commerce feature migrations are applied in order after the base
schema:

- `000027_inventory_reservations` — inventory reservation support.
- `000028_customer_operations` — returns, refunds, and support tickets.
- `000029_tenant_memberships` — tenant membership and role enforcement.
- `000030_campaigns` — voucher/flash-sale campaign data.
- `000031_privacy_preferences` — persisted account privacy controls.
- `000032_payment_proofs` — private payment-proof storage.
- `000033_flash_sale_cart_items` — campaign-aware cart reservations.
- `000034_flash_sale_order_items` — atomic flash-sale quota reservations tied to orders.

Payment instructions use the merchant values in `.env`/`.env.example`:
`PAYMENT_BANK_NAME`, `PAYMENT_BANK_ACCOUNT_NAME`,
`PAYMENT_BANK_ACCOUNT_NUMBER`, and optional `PAYMENT_QR_URL`.

### Transactional Email
Password reset and email verification links are only delivered when SMTP is
configured. Without `SMTP_HOST` the API still starts, but logs a warning and
drops the messages — the tokens are created and nobody receives them.

```bash
SMTP_HOST=smtp.example.com
SMTP_PORT=587            # 465 with SMTP_IMPLICIT_TLS=true
SMTP_USERNAME=...        # optional, omit for an unauthenticated relay
SMTP_PASSWORD=...
SMTP_FROM=no-reply@toko.example
PUBLIC_BASE_URL=https://toko.example   # storefront origin used to build the links
```

`PUBLIC_BASE_URL` matters: without it the emailed links are relative and no mail
client can follow them.

When `APP_ENV=production`, startup rejects unsafe or incomplete configuration.
Use a random `JWT_SECRET` of at least 32 bytes, set
`REFRESH_COOKIE_SECURE=true`, provide HTTPS `PUBLIC_BASE_URL` and
`CORS_ALLOWED_ORIGINS`, disable `PAYMENT_SANDBOX`, and configure the selected
payment, shipping, and SMTP providers with real credentials. TLS verification
must remain enabled.

### Deployment
The image builds three entrypoints — `/app/api`, `/app/worker` and
`/app/migrate`, and runs as an unprivileged user. Build and push an immutable
release tag, then replace `RELEASE_TAG` in the three Kubernetes manifests with
that same tag. Run migrations to completion before rolling out:

```bash
kubectl create -f deploy/k8s/migrate-job.yaml
kubectl wait --for=condition=complete --timeout=300s job -l app=backend-toko-migrate
kubectl apply -f deploy/k8s/deployment.yaml -f deploy/k8s/worker.yaml
```

The migration Job is deliberately a Job and not an initContainer: initContainers
run once per pod, so replicas would race concurrent migrations against one
database.
