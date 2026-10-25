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
- Kinds: `webhook-delivery`, `email-delivery`, `push-delivery` (`GET /api/v1/admin/queue/dlq?kind=email-delivery`).
- `email-delivery` / `push-delivery` payloads are only `{eventId, topic}`; the worker re-renders recipient and copy from `domain_events` on replay, so replay is safe and sends to the rightful recipient. `lastError` is redacted (failure class, SMTP code, relay host or push-service host); the full error is in the worker logs (search by `event_id`).
- Email tasks land in the DLQ after `QUEUE_MAX_ATTEMPTS` for transient SMTP failures (network, 4xx). Invalid recipients and 5xx RCPT replies are dropped with a `Warn` log instead (retrying cannot succeed). Push tasks: transport errors, 429, 5xx and server-side send errors (e.g. VAPID keys the push library cannot use) retry and then dead-letter, never pruning; 404/410, unusable subscription keys and endpoints resolving to non-public addresses prune the subscription; other 4xx are dropped. A task that reached at least one of the user's devices is done (not retried for the failed ones). Completed email/push tasks keep their dedup key for `OUTBOX_RELAY_MAX_AGE_SEC`, so outbox re-dispatches do not send them again.
- Email and push are delivered only when `cmd/worker` runs with `SMTP_*` (and `PUSH_VAPID_*`) set.

## Outbox relay (undispatched domain events)
- `domain_events.dispatched_at IS NULL` means fan-out (webhook scheduling, in-app notification, email/push enqueue) has not completed. `cmd/worker` re-dispatches such rows older than `OUTBOX_RELAY_MIN_AGE_SEC` (30) and younger than `OUTBOX_RELAY_MAX_AGE_SEC` (86400) every `OUTBOX_RELAY_INTERVAL_SEC` (15), up to `OUTBOX_RELAY_MAX_ATTEMPTS` (10). Replicas share one sweep per interval: each tick tries a non-blocking Redis lease `lock:outbox:relay` (TTL 0.9 x interval, never released early) and skips the sweep when another replica holds it, so `MAX_ATTEMPTS` x interval (~150s by default) is the retry budget regardless of replica count.
- Stuck events: `SELECT id, topic, occurred_at, dispatch_attempts, last_dispatch_error FROM domain_events WHERE dispatched_at IS NULL ORDER BY occurred_at DESC LIMIT 50;`. After fixing the cause (usually Redis), reset the counter to let the relay retry: `UPDATE domain_events SET dispatch_attempts = 0 WHERE id = '<id>';` (only events younger than the max age are swept).
- Rows written before migration 000047 or by old binaries during a rollout get `dispatched_at = now()` from the column default and are never re-dispatched.

## Migrations 000047 / 000048 (outbox columns, notifications unique index)
- 000047 adds `domain_events.dispatched_at` (`DEFAULT now()`), `dispatch_attempts`, `last_dispatch_error` and `notifications.event_id`. Each `ALTER` runs in its own explicit transaction with `SET LOCAL lock_timeout = '5s'`; both are metadata-only (no rewrite, no backfill). golang-migrate marks version 47 dirty *before* running the file, so **any** failure (a lock timeout on either `ALTER`, a dropped connection) leaves version 47 dirty and retries (including the k8s migrate Job's `backoffLimit`) fail with `Dirty database version 47`. Recovery is always: `./bin/migrate.new force 46`, then `up` again (the `IF NOT EXISTS` clauses skip whatever committed).
- 000048 builds `uq_notifications_event_user` with `CREATE UNIQUE INDEX CONCURRENTLY` (never blocks reads or writes). If the build fails or is interrupted, version 48 is dirty and Postgres may leave an INVALID index: run `./bin/migrate.new force 47`, then `psql "$DATABASE_URL" -c 'DROP INDEX CONCURRENTLY IF EXISTS uq_notifications_event_user;'`, then `up` again. Check validity with `SELECT indisvalid FROM pg_index WHERE indexrelid = 'uq_notifications_event_user'::regclass;`.
- The new binaries need both versions: `InsertDomainEvent` names the new columns and `CreateNotification` uses `ON CONFLICT (event_id, user_id) WHERE event_id IS NOT NULL`. Do not restart api/worker on the new binaries until version 48 is clean (`deploy.yml` already restarts only after a successful migration).
- Rollback order: roll the api/worker binaries back **first**, then `migrate down` (48, then 47). Running `down` under the new binaries breaks event inserts.
- Rollback strands queued work: the old worker does not consume `email-delivery` / `push-delivery` tasks and has no outbox relay. Either drain first (stop the api, let the new worker run until `GET /api/v1/admin/queue/stats?kind=email-delivery` and `?kind=push-delivery` show nothing ready or in flight and `SELECT count(*) FROM domain_events WHERE dispatched_at IS NULL` is 0, then roll back) or accept that those emails/pushes are lost. Tasks left in Redis stay there; re-deploying the new worker later delivers them (only events younger than `OUTBOX_RELAY_MAX_AGE_SEC` are re-dispatched).

## Scaling
- Tambah replicas API/worker; pantau queue_depth & webhook latency p95.

## Drain & Rolling Update
- Set readiness=false, tunggu job selesai, deploy, verifikasi health & alerts clear.

## Migration 000040 lock timeout
Migration `000040_flash_sale_order_items_item_index` builds `idx_flash_sale_order_items_item` with a plain `CREATE INDEX` under `SET LOCAL lock_timeout = '5s'`. `deploy.yml` runs it while the old api/worker still serve checkout traffic, so a long settlement transaction on `flash_sale_order_items` can make it time out.
- Symptom: the deploy job fails with `up: migration failed: canceling statement due to lock timeout`; any later `up` reports `Dirty database version 40. Fix and force version.`. The restart steps do not run, so the old binaries keep serving. No index is left behind (the whole file runs in one implicit transaction).
- Recovery on the host: `./bin/migrate.new force 39` (or `./bin/migrate force 39`; both work), then re-run the deploy workflow off-peak. If you run `./bin/migrate.new up` by hand instead, finish the remaining deploy steps too: `mv bin/api.new bin/api && mv bin/worker.new bin/worker && mv bin/migrate.new bin/migrate`, then `sudo systemctl restart toko-api toko-worker`.
- Do **not** use `./bin/migrate up`: `deploy.yml` renames `migrate.new` to `migrate` only after a successful migration, so after a failed deploy `bin/migrate` is the old binary, which embeds migrations only up to 39, and `up` is a silent no-op.
