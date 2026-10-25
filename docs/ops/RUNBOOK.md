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
