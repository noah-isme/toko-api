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
- Email tasks land in the DLQ after `QUEUE_MAX_ATTEMPTS` for transient SMTP failures (network, 4xx). Invalid recipients and 5xx RCPT replies are dropped with a `Warn` log instead (retrying cannot succeed). Push tasks: transport errors, 429, 5xx and server-side send errors (e.g. VAPID keys the push library cannot use) retry and then dead-letter, never pruning; 404/410 and unusable subscription keys prune the subscription; other 4xx, and push hosts that resolve to an address the outbound SSRF guard refuses, are dropped with a `Warn` log (`push send failed; not retrying`, with `push_host`) and the subscription is kept. That last case means the worker's DNS is answering with a blocked range (DNS64/NAT64 `64:ff9b::/96`, service-mesh VIPs in `240.0.0.0/4`, fake-IP proxies in `198.18.0.0/15`); fix the worker's egress/DNS, not the subscriptions. A task that reached at least one of the user's devices is done (not retried for the failed ones). Email/push tasks keep their dedup key for `OUTBOX_RELAY_MAX_AGE_SEC` (not `IDEMPOTENCY_TTL_SEC`) while queued or retrying and again after completion, so outbox re-dispatches do not queue or send them again even when the worker is backlogged.
- Email and push are delivered only when `cmd/worker` runs with `SMTP_*` (and `PUSH_VAPID_*`) set.

## Outbox relay (undispatched domain events)
- `domain_events.dispatched_at IS NULL` means fan-out (webhook scheduling, in-app notification, email/push enqueue) has not completed. `cmd/worker` re-dispatches such rows older than `OUTBOX_RELAY_MIN_AGE_SEC` (30) and younger than `OUTBOX_RELAY_MAX_AGE_SEC` (86400) every `OUTBOX_RELAY_INTERVAL_SEC` (15), up to `OUTBOX_RELAY_MAX_ATTEMPTS` (10). Replicas share one sweep per interval: each tick tries a non-blocking Redis lease `lock:outbox:relay` (TTL 0.9 x interval, never released early) and skips the sweep when another replica holds it, so `MAX_ATTEMPTS` x interval (~150s by default) is the retry budget regardless of replica count. This assumes a sweep finishes within the lease; a sweep slower than 0.9 x interval can overlap another replica's sweep. That is safe (fan-out is idempotent per event) but may count two attempts for one round and shorten the budget, so raise `OUTBOX_RELAY_INTERVAL_SEC` if sweeps are slow.
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

## Migrations 000041/000042 (analytics)
`deploy.yml` runs `./bin/migrate.new up` before it restarts `toko-api`/`toko-worker`, so both migrations execute while the old binaries serve checkout traffic.

### 000041 `idx_carts_tenant_created` (CREATE INDEX CONCURRENTLY)
The up file is a single `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_carts_tenant_created ON carts(tenant_id, created_at)` statement (golang-migrate sends a single-statement file without `BEGIN`, which `CONCURRENTLY` requires). It does not block cart writes, but it waits for every transaction that is already open when it starts, so a stuck long transaction makes it hang rather than fail.
- Symptom: the deploy job fails with `up: migration failed: ...` (for example `canceling statement due to statement timeout`, or a deadlock/cancel), and any later `up` reports `Dirty database version 41. Fix and force version.`. A failed concurrent build can leave an **INVALID** `idx_carts_tenant_created` behind; a later `IF NOT EXISTS` would then silently skip it and keep the unusable index.
- Recovery on the host, in this order:
  1. `psql "$DATABASE_URL" -c 'DROP INDEX CONCURRENTLY IF EXISTS idx_carts_tenant_created'` (removes an INVALID leftover; check with `SELECT indisvalid FROM pg_index WHERE indexrelid = 'idx_carts_tenant_created'::regclass`).
  2. `./bin/migrate.new force 40`.
  3. Re-run the deploy workflow (off-peak if a long transaction caused it).

### 000042 drop `mv_sales_daily` / `mv_top_products`
The file starts with `SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '120s';`. `DROP MATERIALIZED VIEW` takes `ACCESS EXCLUSIVE` on views the old binaries' `/api/v1/analytics/*` endpoints still read, so a long read can make it time out.
- Symptom: `up: migration failed: canceling statement due to lock timeout` (or `statement timeout`); later `up` reports `Dirty database version 42. Fix and force version.`. The whole file runs in one implicit transaction, so both views are still there and the old binaries keep working.
- Recovery on the host: `./bin/migrate.new force 41`, then re-run the deploy workflow off-peak.

For both: if you run `./bin/migrate.new up` by hand instead of re-running the workflow, finish the remaining deploy steps (`mv bin/api.new bin/api && mv bin/worker.new bin/worker && mv bin/migrate.new bin/migrate`, then `sudo systemctl restart toko-api toko-worker`). Do **not** use `./bin/migrate up` for the reason given in the 000040 section (after a failed deploy `bin/migrate` is the old binary and `up` is a silent no-op).

## Migrations 000043/000044 lock timeout
`000043_payments_partial_refunds` adds the `PARTIALLY_REFUNDED` label to `payment_status`, the `payments.refunded_amount` column and a `NOT VALID` CHECK; `000044_refunds_lifecycle` validates that CHECK and adds the refund lifecycle columns (`provider_status`, `failure_reason`, `settled_at`, `updated_at`, `attempt`, `payment_id`) to `refunds`. Every file starts with `SET LOCAL lock_timeout = '5s'`. `payments` is hot (checkout, webhooks, status polling) and `deploy.yml` runs these while the old api/worker still serve traffic, so a long transaction on `payments` or `refunds` can make them time out instead of queueing every payment query.
- Symptom: the deploy job fails with `up: migration failed: canceling statement due to lock timeout`; any later `up` reports `Dirty database version 43` (or `44`) `. Fix and force version.`. The restart steps do not run, so the old binaries keep serving. Nothing from the failing file is applied (each file runs in one implicit transaction); 000043 applied in full if the dirty version is 44.
- Recovery on the host: `./bin/migrate.new force 42` when dirty at 43, `./bin/migrate.new force 43` when dirty at 44, then re-run the deploy workflow off-peak. Same manual-finish steps as for 000040 if you run `./bin/migrate.new up` by hand.
- Do **not** use `./bin/migrate up` after a failed deploy: it is the old binary and does not embed these migrations.
- Rolling back 000043 cannot remove the `PARTIALLY_REFUNDED` enum label (PostgreSQL limitation); the down migration resets such payments to `PAID`. Re-running up is safe.
- A refund stuck with an unknown gateway outcome (`refunds.status='PENDING'`, `provider_status='UNKNOWN'`, `settled_at IS NULL`) blocks `PATCH /admin/returns/{id}` with `409 RETURN_REFUND_PENDING`. Resolve it by retrying the refund (it checks or re-sends the same gateway key), or, after confirming in the gateway dashboard that no refund exists, set the row to `status='FAILED'` by hand.

## Migrations 000045 / 000046 lock timeout
Both files start with `SET LOCAL lock_timeout = '5s';` and run while the old api/worker still serve traffic. `000045_order_status_expired` runs `ALTER TYPE order_status ADD VALUE 'EXPIRED'`; `000046_shipment_tracking_poll` adds nullable/defaulted `track_*` columns to `shipments` (metadata-only, no rewrite). Neither creates an index.
- Symptom: the deploy fails with `canceling statement due to lock timeout`; a later `up` reports `Dirty database version 45` (or `46`). The restart steps do not run, so the old binaries keep serving. Nothing is half-applied (each file is one implicit transaction).
- Recovery on the host: `./bin/migrate.new force 44` for a dirty 45, or `./bin/migrate.new force 45` for a dirty 46, then re-run the deploy off-peak. If you run `./bin/migrate.new up` by hand, finish the remaining deploy steps (`mv bin/*.new`, `sudo systemctl restart toko-api toko-worker`) as described for 000040.
- Do **not** use the old `./bin/migrate`; after a failed deploy it embeds migrations only up to 44 and `up` is a silent no-op.
- `000045` down converts `EXPIRED` orders to `CANCELLED`; the enum label itself stays (Postgres cannot drop enum values).

## Periodic jobs (worker)
`cmd/worker` runs two jobs when `WORKER_JOBS_ENABLED=true`: `checkout_expiry` (expire unpaid orders, sweep orphaned reservations) and `shipment_tracking` (poll the carrier). Each tick takes the Redis lock `lock:job:<name>`, so only one worker replica runs a job at a time.

**Confirm runs.** Every run logs `"message":"job run"` with `job`, `status` (`success`/`error`), `duration_ms` and `counters` (e.g. `orders_expired`, `orders_expired_silent`, `orders_expired_proof_unreviewed`, `skipped_proof_hold`, `skipped_renewed`, `no_tracking_data`, `reservations_expired`, `reservations_anomalous`, `polled`, `events_applied`, `errors`, `skipped_breaker`, `deferred_cap`, `stopped_*`). Replicas that lost the lock log `job run skipped` with `reason=lock_held` at debug level. The runner logs `jobs heartbeat` every `WORKER_HEARTBEAT_SEC` with each job's `last_run_age_sec` and `last_status`. Metrics (`worker_job_runs_total`, `worker_job_last_success_timestamp_seconds`, ...) exist but the standalone worker has no `/metrics` listener yet; use the logs.

**Old Midtrans intents.** Intents created by the previous release were sent to Midtrans without an expiry, so a VA / bank-transfer intent stays payable at Midtrans for about 24 h although its local `payments.expires_at` is creation + 15 min. The job treats a `PENDING` Midtrans payment without `response.ExpirySynced` in `provider_payload` as payable until `created_at + 24 h` (+ grace), so those orders are not expired early and no wait is needed before enabling. Orders still inside that window:
```sql
SELECT count(*) FROM payments
WHERE provider = 'midtrans' AND status = 'PENDING'
  AND (provider_payload -> 'response' ->> 'ExpirySynced') IS DISTINCT FROM 'true'
  AND created_at > now() - interval '24 hours';
```
Their stock stays reserved until the window closes (at most 24 h after the deploy).

**First rollout (this release).** `WORKER_JOBS_ENABLED` defaults to `false` and `deploy/k8s/worker.yaml` pins it to `"false"`. Old API binaries settle `PAID` callbacks with an unconditional update, which would move an order the worker just expired back to `PAID` after its stock was returned (oversell). So:
1. Deploy the release with jobs disabled. With `deploy.yml` (systemd) both services restart on the new binaries in the same step; with k8s wait until `kubectl rollout status deployment/backend-toko` reports every API pod on the new image.
2. Check the backlog size:
   ```sql
   -- unpaid orders that will expire on the first tick (deadline + 10 min grace passed;
   -- old Midtrans intents count as payable for 24 h, see above; proof holds not applied)
   SELECT o.tenant_id, count(*) FROM orders o
   WHERE o.status = 'PENDING_PAYMENT'
     AND COALESCE(
           (SELECT max(CASE WHEN p.provider = 'midtrans' AND p.status = 'PENDING'
                             AND (p.provider_payload -> 'response' ->> 'ExpirySynced') IS DISTINCT FROM 'true'
                            THEN GREATEST(p.expires_at, p.created_at + interval '24 hours')
                            ELSE p.expires_at END)
            FROM payments p WHERE p.order_id = o.id),
           o.created_at + interval '15 minutes') + interval '10 minutes' < now()
   GROUP BY 1;
   -- of those, silent (no customer notification): deadline older than 24 h
   -- (same query with "< now() - interval '24 hours'"); orders with a payment proof are never silent
   -- shipments the tracker will poll
   SELECT count(*) FROM shipments WHERE COALESCE(last_status, status) IN ('SHIPPED','OUT_FOR_DELIVERY')
     AND COALESCE(tracking_number,'') <> '' AND track_next_poll_at IS NOT NULL;
   ```
3. Enable: set `WORKER_JOBS_ENABLED=true` in `/opt/toko-api/.env` and `sudo systemctl restart toko-worker` (systemd), or change the value in `worker.yaml` and apply it (k8s). Watch the first `job run` lines.

**While jobs are disabled** abandoned checkouts keep their stock reserved indefinitely (checkout no longer sweeps reservations of payable orders), and shipments are tracked only through courier webhooks. Do not leave jobs disabled for long.

**Pause.** Set `WORKER_JOBS_ENABLED=false` and restart the worker. The webhook delivery worker keeps running.

**Tuning.** `JOB_CHECKOUT_EXPIRY_INTERVAL_SEC` (60), `JOB_CHECKOUT_EXPIRY_GRACE_SEC` (600; how long an order stays payable after its deadline), `JOB_CHECKOUT_EXPIRY_SILENT_AFTER_HOURS` (24; older orders expire without notifications, 0 = never silent; orders with a payment proof always notify), `JOB_CHECKOUT_PROOF_HOLD_HOURS` (72), `JOB_BATCH_SIZE` (100), `JOB_LOCK_TTL_SEC` (300; each run is cancelled after 90 % of it so the lock never expires under a running job), `JOB_SHIPMENT_TRACKING_INTERVAL_SEC` (300), `SHIPMENT_TRACK_MAX_CALLS_PER_TICK` (50), `SHIPMENT_TRACK_BACKOFF_BASE_SEC` (900), `SHIPMENT_TRACK_BACKOFF_MAX_SEC` (21600), `SHIPMENT_TRACK_MAX_ATTEMPTS` (200), `SHIPMENT_TRACK_MAX_AGE_DAYS` (30), `SHIPMENT_TRACK_SILENT_AFTER_HOURS` (48). Carrier calls go through the `shipping-track` circuit breaker (`CB_SHIPPING_*`). A carrier answer with no data for a waybill (not scanned yet, unknown waybill) counts as `no_tracking_data`, backs off that shipment, and does not count as a breaker failure.

**Tracking state.** `shipments.track_next_poll_at IS NULL` means polling stopped; `track_last_error` says why (`stopped: delivered`, `stopped: returned`, `stopped: lost`, `stopped: max_attempts`, `stopped: max_age`) or holds the last carrier error. To re-poll a shipment: `UPDATE shipments SET track_next_poll_at = now(), track_attempts = 0, track_last_error = NULL WHERE id = '<id>' AND tenant_id = '<tenant>';`.

**Reservation anomaly.** A `reservation_anomaly` warning / `reservations_anomalous` counter means expired `RESERVED` reservations exist on orders that are `PAID` or later. They are left untouched; investigate the order's settlement (`payment_events`, webhook logs).

## Late payments
A `PAID` provider callback for an order that is already `EXPIRED` or `CANCELLED` records the payment as `PAID` but leaves the order and its (released) stock alone. Midtrans Snap intents now carry an explicit expiry equal to `PAYMENT_INTENT_EXPIRES_MIN`, and older intents without it are kept payable for Midtrans' 24 h default (see "Old Midtrans intents"), so this should be rare: callbacks delayed past the grace period, payments on `CANCELLED` orders, or a Midtrans default window other than 24 h for some method.
- Detection: metric `payment_late_total{provider}`; warn log `payment.late: payment captured for an order that is no longer payable; refund required` with `order_id`, `payment_id`, `tenant_id`; domain events `SELECT * FROM domain_events WHERE topic = 'payment.late' ORDER BY occurred_at DESC;`; or
  ```sql
  SELECT o.id, o.order_number, o.status, p.id AS payment_id, p.provider, p.amount, p.updated_at
  FROM payments p JOIN orders o ON o.id = p.order_id
  WHERE p.status = 'PAID' AND o.status IN ('EXPIRED', 'CANCELLED');
  ```
  In the admin UI the order shows `paymentStatus: PAID` on an `EXPIRED`/`CANCELLED` order.
- The customer is **not** notified automatically (`payment.late` is ops-only). Contact them.
- Refund in the provider dashboard (Midtrans/Xendit), then record it:
  ```sql
  BEGIN;
  UPDATE payments SET status = 'REFUNDED', updated_at = now() WHERE id = '<payment_id>' AND status = 'PAID';
  INSERT INTO payment_events (payment_id, status, payload) VALUES ('<payment_id>', 'REFUNDED', '{"source":"ops","reason":"late_payment"}');
  COMMIT;
  ```
  An admin refund endpoint for this case is a follow-up.

## Manual bank transfer orders
Customers paying by manual transfer upload a proof (`POST /api/v1/payments/{orderId}/proof`). An order with a proof is not expired for `JOB_CHECKOUT_PROOF_HOLD_HOURS` (default 72 h) counted from the **first** proof; re-uploading does not extend the hold. Stock stays reserved meanwhile.
- Confirm a verified transfer with `POST /api/v1/admin/orders/{id}/confirm-payment` (optional `{"note": "..."}`). It settles the order like a provider `PAID` callback (order `PAID`, stock and flash-sale quota committed, voucher settled, `order.paid` email/notification) and is audit-logged. `PATCH .../status` cannot set `PAID`.
- Reject a bogus proof by cancelling: `PATCH /api/v1/admin/orders/{id}/status` with `{"status":"CANCELLED"}`. Its reservations are released by the `checkout_expiry` orphan sweep once they are past their `expires_at`.
- If nobody confirms within the hold, the order expires with the normal `payment.expired` / `order.canceled` notifications (never silently, however old the order is). Because the customer very likely sent money, the job also logs the warning `payment.proof_unreviewed: order expired with an unconfirmed transfer proof; review the transfer and refund if received` (with `order_id`, `tenant_id`, `proof_count`, `first_proof_at`), counts `orders_expired_proof_unreviewed`, and emits the ops-only domain event `payment.proof_unreviewed` (no customer email or in-app notification). Check the bank statement for each one; refund received transfers by hand and contact the customer:
  ```sql
  SELECT aggregate_id AS order_id, payload->>'orderNumber' AS order_number, payload->>'firstProofAt' AS first_proof_at, occurred_at
  FROM domain_events WHERE topic = 'payment.proof_unreviewed' ORDER BY occurred_at DESC;
  ```
- Proof uploads for non-pending orders return `409 INVALID_STATE`.

## Migrations 000049/000050 lock timeout (search & popularity)
Both files start with `SET LOCAL lock_timeout = '5s'` and run as one implicit transaction each (golang-migrate sends each file as a single multi-statement query), so every lock a file takes is held until that file commits. `lock_timeout` only limits how long a statement waits to *acquire* a lock, not how long it is held. Failure mode and recovery on a lock timeout are the same as for 000040.
- `000049_products_search_trgm_simple`: DDL only, no work proportional to the number of products. It runs `CREATE EXTENSION IF NOT EXISTS pg_trgm` (a trusted extension, so the database owner can create it without superuser), adds the nullable column `products.search_vector_simple` (metadata-only, no table rewrite), sets it and the legacy `search_vector` to `STORAGE MAIN` (metadata-only), creates the function `products_search_vector_simple_refresh()` and the row trigger `trg_products_search_vector_simple` (`BEFORE INSERT OR UPDATE OF title, description`).
  - Lock footprint: `ADD COLUMN`/`SET STORAGE` take ACCESS EXCLUSIVE on `products`, which **blocks product reads as well as writes** until the file commits, and `CREATE TRIGGER` takes SHARE ROW EXCLUSIVE. Because the file contains no `UPDATE` and no `CREATE INDEX`, that window is milliseconds (measured ~4 ms for the whole file on 20k products), plus however long it waits to acquire the lock (at most 5 s, then it fails cleanly).
  - Each `products` DDL statement is guarded by a catalog check (`pg_attribute.attstorage`, `pg_trigger`), so when the column, storage setting and trigger already exist the file takes **no lock on `products` at all**.
- `000050_products_search_index_product_sales`: backfills `search_vector_simple` for rows written before the trigger existed (one `UPDATE ... WHERE search_vector_simple IS NULL`), builds the GIN indexes `idx_products_search_simple` (on the column) and `idx_products_title_trgm`, then creates the `product_sales` table.
  - Lock footprint: never ACCESS EXCLUSIVE on `products`. Before the backfill the file takes SHARE on `products` (`LOCK TABLE products IN SHARE MODE`, the same lock the index builds need), so a product write either finishes before the migration starts its backfill or waits for it; without that lock a write waiting on an already-backfilled row and the later `CREATE INDEX` deadlock, golang-migrate stops dirty at 50 and the write may fail with a 500. If an admin edit holds its transaction open for more than 5 s when the file starts, the file fails with `lock timeout` instead (recover as below). The backfill takes ROW EXCLUSIVE and row-locks every row it fills, the index builds take SHARE, and the `product_sales` FK takes SHARE ROW EXCLUSIVE, all until commit. **Storefront reads (list, detail, cart, checkout) continue; product writes (stock flag updates, admin catalog edits) wait until the file commits.** Measured on 20k products with ~600-character descriptions: about 5 s on a loaded host, about 1.2 s on an idle one; the cost grows linearly with the catalog.
  - The backfill and index builds are skipped when a valid index of that name already exists. An INVALID index (left by a failed `CREATE INDEX CONCURRENTLY`) makes the file fail with `products search index is INVALID ...`: run `DROP INDEX CONCURRENTLY <name>;`, rebuild it as in step (3) below, `./bin/migrate.new force 49`, then re-run the deploy.
  - After the default path, the backfill has left one dead tuple per product. Run `VACUUM ANALYZE products;` once the deploy is done (autovacuum gets there eventually, but list/count/facet scans read the bloated table until it does).
- Large catalog (**mandatory above ~20k products**, i.e. when pausing product writes for more than ~5 s is not acceptable; optional below): make 000050's backfill and index builds a no-op by doing them by hand, with no long lock, before the deploy. (1) By hand, run the statements of `migrations/000049_products_search_trgm_simple.up.sql` in one transaction (`BEGIN;` file contents `COMMIT;`; ACCESS EXCLUSIVE for milliseconds). Do not touch `schema_migrations`: the deploy later re-runs 000049, which then finds everything in place and takes no lock. From then on every product write fills the column. (2) Backfill in batches until it reports `UPDATE 0`: `UPDATE products SET search_vector_simple = setweight(to_tsvector('simple', coalesce(title, '')), 'A') || setweight(to_tsvector('simple', coalesce(description, '')), 'B') WHERE id IN (SELECT id FROM products WHERE search_vector_simple IS NULL LIMIT 1000);` Then confirm `SELECT count(*) FROM products WHERE search_vector_simple IS NULL;` returns 0: **000050 skips the backfill once the index exists**, so the index must be built only after this reaches 0. (3) `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_products_search_simple ON products USING GIN (search_vector_simple);` and `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_products_title_trgm ON products USING GIN (title gin_trgm_ops);`, then check both are valid (`SELECT indexrelid::regclass, indisvalid FROM pg_index WHERE indrelid = 'products'::regclass;`). (4) `VACUUM ANALYZE products;`. The deploy's `migrate up` then takes no lock on `products` in 000049 and only the brief `product_sales` FK lock in 000050.
  - Why a stored column and not an expression index: broad terms (`dan`, `untuk`, category words; the `simple` config has no stop words) match most of a catalog, so Postgres scans the table and would rebuild two tsvectors per row for the filter and for `ts_rank`. Measured on the 20k-product tenant (count / list / price facet, ms): term in 100% of products, expression index 407 / 793 / 426, stored column 21 / 42 / 34, base branch (stored english column) 19 / 44 / 33; term in ~67%: 405 / 662 / 401 vs 5 / 27 / 18; term in 10%: 4 / 74 / 5 vs 1.5 / 7 / 3.
  - Why `STORAGE MAIN`: two vectors plus a long description push a row past the ~2 kB TOAST target. A vector left `EXTENDED` is moved out of line and every sequential-scan match then pays a TOAST fetch (about 6x slower on broad terms, for whichever vector was moved). With both on `MAIN` the description is moved out of line first, and both the new binary and old binaries keep inline vectors. A `MAIN` value itself is moved out of line only as a last resort, when the row still exceeds ~8 kB (one page), so catalogs with very long (e.g. HTML) descriptions get wider heap rows than before. Non-search list/count/facet timings were measured only with ~600-character descriptions; re-check them on such a catalog.
- The english `products.search_vector` column and `idx_products_search` are **kept on purpose**: the old binaries query them during the deploy window and after an app-only rollback. A follow-up migration in a later release drops them.
- The trigger keeps `search_vector_simple` correct for writes from old binaries too, so the app can be rolled back and forward freely. Rolling back the migrations: `migrate down` from 50 drops `product_sales` and both new indexes (DROP INDEX takes a brief ACCESS EXCLUSIVE); down from 49 drops the column, trigger and function and resets `search_vector` to `EXTENDED`.
- `product_sales` (created at the end of 000050) has no triggers and no backfill, so the migration never locks `orders` or `order_items`. The worker fills the table on its first popularity refresh after restart.
- Recovery after a lock timeout: `./bin/migrate.new force <previous version>` (the version before the failing file), then re-run the deploy off-peak, as for 000040.

## Popularity refresh job (`product_sales`)
- The worker (`cmd/worker`) recomputes `product_sales` from `order_items JOIN orders` once at startup and then every `PRODUCT_SALES_REFRESH_SEC` seconds (default 300; `0` disables the job). Each tenant is refreshed in its own short transaction, guarded by a transaction-scoped advisory lock (`pg_try_advisory_xact_lock`), so several worker replicas never refresh the same tenant concurrently. A replica that finds the lock taken skips that tenant.
- Paid statuses that count: `PAID`, `PACKED`, `SHIPPED`, `OUT_FOR_DELIVERY`, `DELIVERED`. Cancelled or unpaid orders do not count, and rows for products with no paid sales are deleted.
- The payment webhook never touches `product_sales`. This is deliberate: a counter row updated inside the settlement transaction would serialise every paid webhook for a popular product and could exhaust the API's DB pool (see `internal/payment/webhook_concurrency_integration_test.go`).
- Signal: the worker logs `job=product_sales_refresh` with `tenants`, `skipped`, `upserted`, `deleted` and `duration` on every run, or `popularity refresh failed` at error level. `skipped` counts tenants left for the next run: another replica held the tenant's lock, or a product was deleted while the refresh ran (logged as `product deleted during popularity refresh; tenant deferred to next run`; the upsert takes no row lock on `products`, so the FK check fails with 23503 instead). A tenant that is skipped on every run points to constant product deletes during the refresh.
- Manual repair: `product_sales` is derived data. Wait for the next run or restart the worker; `TRUNCATE product_sales;` followed by a worker restart rebuilds it from scratch. Never edit its rows by hand.

## Search scaling limits (`GET /api/v1/products?q=`)
- `q` is capped at 200 characters (400 above). The trigram fuzzy fallback and suggestions run only for 3-64 characters, because their cost grows with `len(q)` (measured on 20k products: a 2,000-character `q` took 4.7 s for the fuzzy count and the same again for suggestions).
- The fuzzy pass (count, list, facets, suggestions) runs in one read-only transaction with `statement_timeout = 500ms` per statement (`catalog.DefaultFuzzyStatementTimeout`) and a 1 s deadline for the whole pass (`catalog.DefaultFuzzyPassTimeout`), so one request holds a pool connection for at most about 1 s. When either fires (and the client is still connected), the API logs `catalog fuzzy search timed out; returning full-text result` at warn level and returns the empty full-text result; the request does not fail. Frequent occurrences mean the catalog has outgrown the cold path below.
- Suggestions (`meta.suggestions`) run only after both full-text and fuzzy search found nothing. For junk input the trigram index answers in under 1 ms. For a query whose words share trigrams with many titles (for example a 3-word typo of common words) Postgres scans the tenant's titles: about 100-120 ms on a 20k-product tenant, growing linearly. Every zero-result search of 3-64 characters can pay this. If a tenant grows far beyond that, consider a dedicated suggestions table or caching suggestions per normalised `q`.
- Broad full-text terms are served by the stored `search_vector_simple` column (see the 000049 section); at 20k products the worst case measured was ~42 ms for the list query and ~21 ms per count/facet query.

## Migrations 000051/000052 lock timeout
`000051_product_alerts` (alert subscription/outbox tables plus triggers on `products` and `product_variants`) and `000052_order_invoices` (`invoice_counters`, `order_invoices`) both start with `SET LOCAL lock_timeout = '5s'` in up **and** down, and `deploy.yml` runs them while the old api/worker still serve traffic. 000051 takes SHARE ROW EXCLUSIVE on `products` / `product_variants` (`CREATE TRIGGER`, FKs from the new tables) and on `domain_events` / `tenants` (FKs from the empty `product_alert_deliveries`). 000052 takes it on `orders` and `tenants` for the FK from the empty `order_invoices`. A long settlement transaction can make either one time out. Neither migration alters `orders` columns or builds an index on a populated table.
- Symptom: the deploy fails with `up: migration failed: canceling statement due to lock timeout`, and later `up` reports `Dirty database version 51` (or `52`). The old binaries keep serving. Nothing is left behind, because each file runs as one implicit transaction with no `CONCURRENTLY` and so no INVALID index.
- Recovery: `./bin/migrate.new force 50` when 51 failed, or `./bin/migrate.new force 51` when 52 failed, then re-run the deploy off-peak. The same `migrate.new` vs `migrate` caveat as 000040 applies.
- Rollback: `down` for 000052 drops `order_invoices` / `invoice_counters` (issued invoice numbers are lost). `down` for 000051 drops the triggers, functions and alert tables (pending subscriptions are lost). Run them only after rolling back the code that queries those tables. Old binaries never reference them, so leaving the schema in place is safe.

## Product alerts drainer
Back-in-stock / price-drop alerts are handled by two goroutines inside each API process, both `alerts.Runner` instances: the **drainer** (`alerts.Dispatcher`) and the **mailer** (`alerts.Mailer`). The drainer runs every 15s, and right away after admin stock/price edits, order cancels and subscribes that race a restock. Several replicas are safe: events and subscriptions are claimed with `FOR UPDATE SKIP LOCKED` and the `notified_at IS NULL` guard, and email rows are leased.
- Drainer: one pass claims up to 200 subscriptions, split evenly across the (up to 100) pending events in the batch. Inside one transaction it writes `notified_at`, the in-app notification, the `domain_events` row (`topic LIKE 'alert.%'`) and a `product_alert_deliveries` row. It never waits on SMTP. An event with more claimable subscriptions than its share stays pending with `last_attempt_at` set, which moves it behind events not yet attempted (`ORDER BY last_attempt_at NULLS FIRST, id`).
- Backlog: `SELECT count(*), min(created_at), count(last_attempt_at) AS rotating FROM product_alert_events WHERE processed_at IS NULL;`. A growing backlog with no `rotating` rows means the drainer is failing; look for `drain product alerts` errors.
- Mailer: leases up to 20 due rows from `product_alert_deliveries`, sends at most 4 at a time through the existing email notifier, and marks each delivered or schedules a retry (backoff 1m, 2m, 4m ... capped at 1h; gives up after 6 attempts and sets `failed_at`). The per-send bound is `2 x SMTP_TIMEOUT_SEC + 5s` because `SMTPSender` applies its timeout to the dial and to the conversation separately; the lease covers a whole pass, so a crash leaves rows that become due again once the lease expires. Delivery is at-least-once.
- Email backlog: `SELECT count(*) FILTER (WHERE delivered_at IS NULL AND failed_at IS NULL) AS pending, count(*) FILTER (WHERE failed_at IS NOT NULL) AS abandoned, max(attempts) FROM product_alert_deliveries;`. Errors are logged as `deliver product alert email`; `last_error` holds the latest one. To resend abandoned rows after fixing the relay: `UPDATE product_alert_deliveries SET failed_at = NULL, attempts = 0, next_attempt_at = now() WHERE failed_at IS NOT NULL;`.
- Retention: processed `product_alert_events` and delivered/abandoned `product_alert_deliveries` older than 7 days are deleted in batches of 1000, at most once an hour per process. `domain_events` rows are not pruned by this feature.
- To stop email for alerts only, set `NOTIFY_EMAIL_TOPIC_ALERT_BACK_IN_STOCK=false` / `NOTIFY_EMAIL_TOPIC_ALERT_PRICE_DROP=false` (deliveries are then marked delivered without sending). In-app notifications are unaffected.

## Migrations 000053/000054 lock timeout (products)
Migrations `000053_product_status` and `000054_product_low_stock_threshold` each run one `ALTER TABLE products ADD COLUMN ... DEFAULT ... CHECK (...)` under `SET LOCAL lock_timeout = '5s'`. The constant default is metadata-only, but the statement needs an `ACCESS EXCLUSIVE` lock on `products` and verifies the inline CHECK in the same statement. `deploy.yml` runs them while the old api/worker still serve storefront traffic, so a long reader on `products` (a slow report, an open transaction) can make them time out.
- Symptom: the deploy job fails with `up: migration failed: canceling statement due to lock timeout`; any later `up` reports `Dirty database version 53. Fix and force version.` (or `54`). The restart steps do not run, so the old binaries keep serving. No column is left behind (each file runs in one implicit transaction).
- Recovery on the host: when 53 failed, `./bin/migrate.new force 52`; when 54 failed, `./bin/migrate.new force 53`. Then re-run the deploy workflow off-peak. If you run `./bin/migrate.new up` by hand instead, finish the remaining deploy steps too: `mv bin/api.new bin/api && mv bin/worker.new bin/worker && mv bin/migrate.new bin/migrate`, then `sudo systemctl restart toko-api toko-worker`.
- Do **not** use `./bin/migrate up`: after a failed deploy `bin/migrate` is the old binary, which does not embed these migrations, and `up` is a silent no-op.
- Rollback order: the new api/worker binaries select `products.status` and `products.low_stock_threshold` in every catalog query. To roll back, **first** restore the previous release binaries and restart `toko-api toko-worker`, **then** run `./bin/migrate down` twice (54, then 53). Running `down` while the new binaries serve makes every catalog request fail. `down` discards draft/archived state (every product becomes visible again on the next `up`, as `published`) and all per-product low-stock thresholds.

## Migration 000055/000056 lock timeout
Migration `000055_tenant_membership_staff_roles` backfills `ADMIN` for global admins (row locks only; `ADMIN` is allowed by the old CHECK) and then replaces the `tenant_memberships` role CHECK (added `NOT VALID`, so no validation scan runs under the ACCESS EXCLUSIVE lock), all under `SET LOCAL lock_timeout = '5s'`. `deploy.yml` runs it while the old api still reads `tenant_memberships` on every authenticated request, so a long transaction touching that table can make the `ALTER TABLE` time out. `000056_tenant_membership_role_check_validate` then runs `VALIDATE CONSTRAINT`, which takes only SHARE UPDATE EXCLUSIVE and does not block the API.
- Symptom: the deploy job fails with `up: migration failed: canceling statement due to lock timeout` or, for `000055`, `up: migration failed: deadlock detected` (the backfill `UPDATE` holds ROW EXCLUSIVE before the `ALTER TABLE` upgrades to ACCESS EXCLUSIVE, so a concurrent writer waiting on a backfilled row can deadlock with it; recovery is the same); any later `up` reports `Dirty database version 55` (or `56` if the `VALIDATE` step failed). The restart steps do not run, so the old binaries keep serving. Nothing is left half-applied: each file runs in one implicit transaction.
- Recovery on the host: for a dirty `55`, `./bin/migrate.new force 54`; for a dirty `56`, force `55`. Then re-run the deploy workflow off-peak. If you run `./bin/migrate.new up` by hand instead, finish the remaining deploy steps as described for 000040.
- `000056` can be retried at any time without impact on traffic.
- Do **not** use `./bin/migrate up`: after a failed deploy `bin/migrate` is the old binary and `up` is a silent no-op (same as 000040).
- Rolling back: `000056` down is a no-op (a validated CHECK cannot be un-validated). `000055` down maps the new staff roles and the backfilled `ADMIN` rows to `CUSTOMER` and restores the three-value CHECK as `NOT VALID`, so afterwards `pg_constraint.convalidated` is `false` for `tenant_memberships_role_check` even though every row satisfies it and new writes are checked. To restore the exact pre-migration state, run `ALTER TABLE tenant_memberships VALIDATE CONSTRAINT tenant_memberships_role_check;` (SHARE UPDATE EXCLUSIVE only, safe under traffic).
