# Roadmap — Toko API

> Last reviewed: 2026-08 · Priorities may shift based on merchant feedback.

## Legend

- ✅ Done
- 🚧 In Progress
- 📋 Planned
- 💡 Under Consideration

---

## Phase 1 — Core Commerce (✅ Complete)

- ✅ User registration, JWT auth, password reset, email verification
- ✅ Product catalog with variants, brands, categories, images
- ✅ Cart lifecycle (anonymous + authenticated, merge on login)
- ✅ Atomic checkout with inventory reservation
- ✅ Payment integration (Midtrans + Xendit)
- ✅ Order management with status transitions
- ✅ Shipping rate estimation + tracking (RajaOngkir)
- ✅ Voucher engine with rules and per-user limits
- ✅ Multi-tenancy with membership checks on protected workflows
- ✅ Schema migrations (embedded in binary)

## Phase 2 — Engagement & Operations (✅ Complete)

- ✅ Product reviews with aggregated stats
- ✅ Product Q&A with voting
- ✅ Favorites / wishlist
- ✅ Loyalty program (points, earn, redeem)
- ✅ Flash-sale campaigns with quota reservation
- ✅ In-app notification centre
- ✅ Web push subscriptions (VAPID)
- ✅ Domain event bus with webhook delivery
- ✅ Admin panel endpoints (catalog, orders, vouchers, analytics)
- ✅ Audit logging with sampling

## Phase 3 — Reliability & Observability (✅ Complete)

- ✅ Prometheus metrics + Grafana dashboards
- ✅ OpenTelemetry distributed tracing
- ✅ Circuit breaker, retry, and timeout wrapper for webhook delivery
- ✅ Rate limiting (global, IP, user, login)
- ✅ Dead-letter queue with admin replay
- ✅ Graceful shutdown with readiness probe
- ✅ k6 load tests and chaos playbooks
- ✅ SLO documentation and alert rules
- ✅ Security hardening (CORS, CSRF, HSTS, body limit)

## Phase 4 — Scale & Developer Experience (📋 Planned)

- 📋 **Search engine integration** — Meilisearch or Elasticsearch for full-text product search, faceted filtering, and typo tolerance. Current Postgres `tsvector` is a bottleneck above ~50k products.
- 📋 **Image upload & CDN** — Product image upload API with S3-compatible storage and CDN-served URLs. Currently images are external URLs seeded in migrations.
- 📋 **OpenAPI auto-generation** — Generate `openapi.yaml` from handler annotations or code. Current spec is maintained by hand.
- 📋 **gRPC internal API** — Optional gRPC surface for service-to-service communication if the monolith is split.
- 📋 **CI/CD hardening** — GitHub Actions already run build/test checks, a Docker image verification job, the tenant guard, a pull-request k6 smoke test, and a production SSH/systemd deployment. Add or refine promotion and staging gates as environments require.

## Phase 5 — Advanced Features (💡 Under Consideration)

- 💡 **Real-time order tracking** — WebSocket or SSE for live order status updates.
- 💡 **Marketplace model** — Multi-vendor support within a tenant (seller accounts, commission).
- 💡 **Recommendation ML** — Replace SQL-based collaborative filtering with a trained model service.
- 💡 **Multi-currency** — Support for international storefronts beyond IDR.
- 💡 **A/B testing framework** — Feature flags and experiment bucketing for promotions.
- 💡 **Scheduled campaigns** — Cron-based flash-sale activation and deactivation.
- 💡 **Exportable reports** — CSV/PDF export for orders, sales, and inventory.

---

## Known Technical Debt

| Area | Issue | Priority |
|------|-------|----------|
| Config | `cmd/api/main.go` is >1100 lines; wiring should move to `internal/app` | Medium |
| Tests | Integration test coverage for checkout + payment webhook flow is thin | High |
| Tenant guard | PR workflow runs `tenant_guard` with statement-level checks for required admin tables, but non-admin SQL, CTEs, inserts, and application-level isolation still need broader coverage | Medium |
| Generated code | `internal/db/gen` is committed; could be generated in CI instead | Low |
| Docs | OpenAPI spec may drift from implementation without automated checks | Medium |
| Dependencies | `go.mod` includes sqlc generation tooling alongside the golang-migrate runtime used by `cmd/migrate`; keep tooling and runtime ownership clear | Low |

## Contributing

See [AGENTS.md](../AGENTS.md) for coding conventions and workflow. New features should:
1. Add a migration if schema changes are needed.
2. Add sqlc queries and regenerate.
3. Add handler + service + tests.
4. Update OpenAPI spec and API contracts.
5. Update this roadmap.
