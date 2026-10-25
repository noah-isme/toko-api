# Runbook

## Incident Tiers & Escalation
- HighErrorRate/HighLatency -> Sev2; BreakerOpenTooLong -> Sev2; DLQSizeHighCrit -> Sev1.

## P1 -- Payment Failover
### Trigger
- Circuit breaker for payment provider (e.g., Midtrans) trips: payment_circuit_open alert fires.
- Payment circuit metrics show the provider is unavailable; `/health/ready` only checks the database and Redis.

### Impact
- New checkouts fail at the payment step; existing paid orders are unaffected.
- Users see "Payment temporarily unavailable" with retry guidance.

### Diagnosis
1. Check provider status page / Midtrans dashboard.
2. Inspect toko_http_request_duration_ms for /checkout -> p99 spike.
3. Verify payment_circuit_open metric = 1 in Prometheus.
4. Check the recent deploy or configuration change for payment credentials.

### Mitigation
- Immediate: Disable checkout at the storefront or ingress while the provider is unavailable; this repository has no runtime `PAYMENT_ENABLED` flag.
- Failover: If a secondary provider is configured (e.g., Xendit), update `PAYMENT_PROVIDER=xendit` and its credentials, then roll out the change.
- Workaround: Instruct users to use bank transfer (VA) which may route through different integration path.

### Resolution
- Root-cause: provider outage / bad credentials / rate limit.
- Reset circuit: breakers transition out of the open state automatically after their configured interval; redeploy after correcting credentials or provider configuration if an immediate reset is required.
- Verify that a smoke checkout succeeds before restoring normal traffic.

### Postmortem Template
- Timeline (UTC): detection -> mitigation -> resolution.
- Affected users / revenue impact.
- Action items: circuit tuning, provider SLA negotiation, secondary provider testing.

---

## P2 -- Flash Sale Exhaustion
### Trigger
- flash_sale_stock_depleted alert fires or admin notices campaign sold out early.
- toko_flash_sale_stock_remaining metric hits 0 for active campaign items.

### Impact
- Campaign shows "Sold Out" prematurely or before scheduled end.
- Customer complaints / support tickets spike.

### Diagnosis
1. GET /api/v1/admin/flash-sales/{id} -> check items[].stock vs items[].stockLimit.
2. Query orders table for flash_sale_id = campaign ID; count paid orders per item.
3. Check for race condition: concurrent checkout requests exceeded stock reservation (review checkout service reservation logic).

### Mitigation
- Extend stock (if inventory allows): PATCH /admin/flash-sales/{id} with updated items[].stockLimit.
- Pause campaign: PATCH /admin/flash-sales/{id} with status: ENDED to stop new orders.
- Communicate: Post banner on storefront; notify waitlist if feature exists.

### Resolution
- If oversold: decide honour / refund per business policy.
- If undersold (bug): fix reservation logic (optimistic lock / Redis atomic decr), redeploy.
- Re-open campaign with corrected stock if within schedule.

### Postmortem Template
- Timeline (UTC).
- Units oversold/undersold per SKU.
- Revenue impact & customer comms sent.
- Action items: load test flash-sale path, add chaos test for concurrent reservation.

---

## P3 -- Tenant Onboarding
### Trigger
- New tenant row inserted via admin API or migration; tenant record exists but storefront returns 404 / no products.

### Impact
- Tenant cannot access admin; storefront shows "Store not found" on subdomain.

### Diagnosis
1. SELECT * FROM tenants WHERE slug = '<tenant-slug>' -> verify status = 'active', domain set.
2. Check DNS: dig <tenant-slug>.toko.com -> CNAME to wildcard cert target.
3. Verify tenant_resolver middleware logs: X-Tenant-ID header / subdomain extraction.
4. Check Redis cache: the configured tenant cache prefix contains the expected tenant mapping.

### Mitigation
- DNS propagation: Wait TTL (typically 60-300s); flush local DNS.
- Cache invalidation: restart API pods or evict the affected tenant cache keys directly; there is no public cache-flush endpoint.
- Status correction: PATCH /admin/tenants/{id} with status: active if stuck in pending.

### Resolution
- Ensure migration 000018+ seeds default tenant correctly.
- Add health endpoint /health/tenant/<slug> for automated verification.
- Document required DNS records in onboarding checklist.

### Postmortem Template
- Timeline (UTC).
- Root cause: DNS / cache / DB status / middleware config.
- Tenants affected.
- Action items: automated tenant verification job, onboarding runbook in admin UI.

---

## DLQ Replay
- Gunakan endpoint admin replay per-id atau batch (kind); pastikan root cause diatasi sebelum replay massal.

## Scaling
- Tambah replicas API/worker; pantau queue_depth & webhook latency p95.

## Drain & Rolling Update
- Set readiness=false, tunggu job selesai, deploy, verifikasi health & alerts clear.

## Migration 000040 lock timeout
Migration `000040_flash_sale_order_items_item_index` builds `idx_flash_sale_order_items_item` with a plain `CREATE INDEX` under `SET LOCAL lock_timeout = '5s'`. `deploy.yml` runs it while the old api/worker still serve checkout traffic, so a long settlement transaction on `flash_sale_order_items` can make it time out.
- Symptom: the deploy job fails with `up: migration failed: canceling statement due to lock timeout`; any later `up` reports `Dirty database version 40. Fix and force version.`. The restart steps do not run, so the old binaries keep serving. No index is left behind (the whole file runs in one implicit transaction).
- Recovery on the host: `./bin/migrate.new force 39` (or `./bin/migrate force 39`; both work), then re-run the deploy workflow off-peak. If you run `./bin/migrate.new up` by hand instead, finish the remaining deploy steps too: `mv bin/api.new bin/api && mv bin/worker.new bin/worker && mv bin/migrate.new bin/migrate`, then `sudo systemctl restart toko-api toko-worker`.
- Do **not** use `./bin/migrate up`: `deploy.yml` renames `migrate.new` to `migrate` only after a successful migration, so after a failed deploy `bin/migrate` is the old binary, which embeds migrations only up to 39, and `up` is a silent no-op.

## Migrations 000051/000052 lock timeout
`000051_product_alerts` (alert subscription/outbox tables plus triggers on `products` and `product_variants`) and `000052_order_invoices` (`invoice_counters`, `order_invoices`) both start with `SET LOCAL lock_timeout = '5s'` in up **and** down, and `deploy.yml` runs them while the old api/worker still serve traffic. 000051 takes SHARE ROW EXCLUSIVE on `products` / `product_variants` (`CREATE TRIGGER`, FKs from the new tables). 000052 takes it on `orders` and `tenants` for the FK from the empty `order_invoices`. A long settlement transaction can make either one time out. Neither migration alters `orders` columns or builds an index on a populated table.
- Symptom: the deploy fails with `up: migration failed: canceling statement due to lock timeout`, and later `up` reports `Dirty database version 51` (or `52`). The old binaries keep serving. Nothing is left behind, because each file runs as one implicit transaction with no `CONCURRENTLY` and so no INVALID index.
- Recovery: `./bin/migrate.new force <previous version>` (the version before 51, or `51` when 52 failed), then re-run the deploy off-peak. The same `migrate.new` vs `migrate` caveat as 000040 applies. These files may be renumbered on integration, so use the number in the error message.
- Rollback: `down` for 000052 drops `order_invoices` / `invoice_counters` (issued invoice numbers are lost). `down` for 000051 drops the triggers, functions and alert tables (pending subscriptions are lost). Run them only after rolling back the code that queries those tables. Old binaries never reference them, so leaving the schema in place is safe.

## Product alerts drainer
Back-in-stock / price-drop alerts are drained by a goroutine inside each API process (`alerts.Runner`). It runs every 15s, and right away after admin stock/price edits and order cancels. Several replicas are safe because events and subscriptions are claimed with `FOR UPDATE SKIP LOCKED` and the `notified_at IS NULL` guard.
- Backlog: `SELECT count(*), min(created_at) FROM product_alert_events WHERE processed_at IS NULL;`. A growing backlog means the runner is failing. Look for `drain product alerts` errors in the API logs.
- Each Flush claims at most `min(200, remaining run time / 20s)` subscriptions, so the worst-case SMTP time for its emails fits inside the 60s run. Emails are sent after commit, each with its own 20s timeout that does not depend on the run deadline. The in-app notification and the `domain_events` row (`topic LIKE 'alert.%'`) commit together with `notified_at`. Failed emails are logged as `deliver product alert` and not retried. An email in flight when the process is killed (after the shutdown grace) is lost, but its in-app notification and domain event remain.
- To stop email for alerts only, set `NOTIFY_EMAIL_TOPIC_ALERT_BACK_IN_STOCK=false` / `NOTIFY_EMAIL_TOPIC_ALERT_PRICE_DROP=false`. In-app notifications are unaffected.
