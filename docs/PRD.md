# Product Requirements Document — Toko

## Problem Statement

Indonesian SMBs and independent sellers need a modern, API-first commerce backend that handles the full purchase lifecycle — from product discovery through payment, shipping, and post-sale support — without requiring custom integrations for each payment gateway, courier, or storefront.

## Target Users

| Persona | Description |
|---------|-------------|
| **Merchant / Tenant Admin** | Manages catalog, vouchers, flash sales, orders, returns, and analytics through the admin API surface. |
| **Shopper** | Browses products, manages cart and wishlist, checks out, tracks orders, leaves reviews, and contacts support. |
| **Frontend Developer** | Integrates via the REST API contracts documented in `docs/contracts/`. |
| **Ops / SRE** | Monitors SLOs, responds to alerts, and operates the platform via runbooks, DLQ management, and Grafana dashboards. |

## Core Capabilities

### 1. Catalog & Discovery
- Products with variants, images, brands, and categories.
- Full-text search with `tsvector` index.
- Personalised and collaborative-filtering recommendations (trending, also-viewed, frequently-bought-together).

### 2. Cart & Checkout
- Anonymous and authenticated carts; cart merge on login.
- Voucher application with per-user limits, stackable rules, and campaign awareness.
- Draft checkout with shipping quote (RajaOngkir) and tax calculation.
- Atomic checkout: inventory reservation and order creation run in a single DB transaction; the payment intent is created immediately after commit, with failure cleanup releasing the reservation and cancelling the order.

### 3. Payments
- Pluggable provider interface; ships with **Midtrans** and **Xendit** adapters plus a stub for tests.
- Idempotent webhook handlers with Redis replay protection.
- Manual payment proof upload flow for bank transfers.

### 4. Shipping & Tracking
- RajaOngkir integration for cost estimation and tracking.
- Inbound courier webhooks for status updates.
- Email notifications on shipped / out-for-delivery / delivered.

### 5. Promotions & Loyalty
- Voucher engine with percentage, fixed, and free-shipping discount types.
- Flash-sale campaigns with per-item quota reservation.
- Points-based loyalty program with earn / redeem / transaction history.

### 6. Multi-Tenancy
- Tenant resolution via `X-Tenant-ID` header or subdomain.
- Membership-gated access: authenticated requests require an `ACTIVE` tenant membership row.
- Per-tenant onboarding flow and settings.

### 7. Customer Operations
- Product reviews with aggregated stats.
- Product Q&A with voting.
- Returns and refunds workflow.
- Support ticket system with threaded messages.
- Favorites / wishlist.

### 8. Notifications & Webhooks
- Domain event bus publishes order, payment, and shipping events.
- Outbound webhook delivery with HMAC signing, exponential back-off, and dead-letter queue.
- In-app notification centre with unread counts.
- Web-push subscriptions (VAPID).
- Transactional email (SMTP) for password reset and email verification.

### 9. Admin & Analytics
- Full CRUD for products, categories, brands.
- Order management with status transitions and shipment creation.
- Voucher and flash-sale management.
- Inventory tracking and adjustment.
- Sales analytics, top products, and materialized-view overview.
- Audit logging with configurable sampling.

### 10. Observability & Resilience
- Prometheus metrics, OpenTelemetry tracing, structured JSON logging (zerolog).
- Circuit breakers and jittered retries for outbound webhook delivery; payment and shipping provider clients use bounded HTTP timeouts.
- Concurrency guard, rate limiting (global / IP / user / login), body-size limit, CSRF, HSTS.
- Graceful shutdown with readiness toggle.

## Success Criteria

| Metric | Target |
|--------|--------|
| HTTP p95 latency | < 250 ms |
| HTTP error rate | < 1 % |
| Webhook dispatch p99 | < 1 500 ms |
| Checkout success rate | > 99 % (non-payment failures) |
| Zero-downtime deploys | Migration job completes before rollout |

## Out of Scope (current phase)

- Frontend / storefront application (separate repo `toko-app`).
- Marketplace multi-vendor model — current model is single-seller-per-tenant.
- Full-text search engine (Elasticsearch / Meilisearch) — search is Postgres `tsvector` only.
- Real-time features (WebSocket / SSE) beyond web-push.

## References

- [API Contracts](contracts/README.md)
- [Quick Start for Frontend](QUICK_START_FRONTEND.md)
- [SLO](ops/SLO.md) · [Runbook](ops/RUNBOOK.md)
- [OpenAPI spec](../openapi.yaml)
