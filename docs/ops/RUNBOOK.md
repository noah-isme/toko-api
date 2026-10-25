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

## Migrations 000049/000050 lock timeout (search & popularity)
Both files start with `SET LOCAL lock_timeout = '5s'` and run as one implicit transaction each, with the same failure mode and recovery as 000040.
- `000049_products_search_trgm_simple` runs `CREATE EXTENSION IF NOT EXISTS pg_trgm` (a trusted extension, so the database owner can create it without superuser), adds the nullable column `products.search_vector_simple` (metadata-only, no table rewrite), sets it and the legacy `search_vector` to `STORAGE MAIN` (metadata-only), installs the row trigger `trg_products_search_vector_simple` (`BEFORE INSERT OR UPDATE OF title, description`, function `products_search_vector_simple_refresh()`), backfills every row whose column is still NULL with one `UPDATE`, and builds two GIN indexes: `idx_products_search_simple` (on the column) and `idx_products_title_trgm`.
  - Lock footprint: `ADD COLUMN`/`SET STORAGE` take a brief ACCESS EXCLUSIVE and `CREATE TRIGGER` a brief SHARE ROW EXCLUSIVE (both bounded by `lock_timeout`). The backfill row-locks every product until commit and the index builds hold SHARE on `products`: storefront reads continue, product writes (stock flag updates, admin catalog edits) wait until the migration commits. `lock_timeout` only limits how long the migration waits to *acquire* a lock, not how long it holds it. Measured on a 20k-product tenant with ~600-character descriptions: about 5 s for the whole file.
  - Large catalog: make the migration a no-op by doing the work by hand before the deploy (every statement in the file is idempotent). (1) Run the file's statements up to and including the `DO $$ ... CREATE TRIGGER ... $$;` block (extension, `ADD COLUMN IF NOT EXISTS`, both `SET STORAGE MAIN`, `CREATE OR REPLACE FUNCTION`, trigger). From then on every product write fills the column. (2) Backfill in batches until it reports `UPDATE 0`: `UPDATE products SET search_vector_simple = setweight(to_tsvector('simple', coalesce(title, '')), 'A') || setweight(to_tsvector('simple', coalesce(description, '')), 'B') WHERE id IN (SELECT id FROM products WHERE search_vector_simple IS NULL LIMIT 1000);` (3) `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_products_search_simple ON products USING GIN (search_vector_simple);` and `CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_products_title_trgm ON products USING GIN (title gin_trgm_ops);`. The migration then finds nothing to backfill and no index to build. Run `VACUUM ANALYZE products;` after a large backfill.
  - Why a stored column and not an expression index: broad terms (`dan`, `untuk`, category words; the `simple` config has no stop words) match most of a catalog, so Postgres scans the table and would rebuild two tsvectors per row for the filter and for `ts_rank`. Measured on the 20k-product tenant (count / list / price facet, ms): term in 100% of products, expression index 407 / 793 / 426, stored column 21 / 42 / 34, base branch (stored english column) 19 / 44 / 33; term in ~67%: 405 / 662 / 401 vs 5 / 27 / 18; term in 10%: 4 / 74 / 5 vs 1.5 / 7 / 3.
  - Why `STORAGE MAIN`: two vectors plus a long description push a row past the 2 kB TOAST threshold. A vector left `EXTENDED` is moved out of line and every sequential-scan match then pays a TOAST fetch (about 6x slower on broad terms, for whichever vector was moved). With both on `MAIN` the description is moved out of line instead, and both the new binary and old binaries keep inline vectors.
- The english `products.search_vector` column and `idx_products_search` are **kept on purpose**: the old binaries query them during the deploy window and after an app-only rollback. A follow-up migration in a later release drops them.
- The trigger keeps `search_vector_simple` correct for writes from old binaries too, so the app can be rolled back and forward freely. Rolling back the migration itself (`migrate down` past 49) drops the column, trigger, function and both new indexes and resets `search_vector` to `EXTENDED`.
- `000050_product_sales` only creates the `product_sales` table (FK to `products` takes a brief SHARE ROW EXCLUSIVE lock). There are no triggers and no backfill, so it never locks `orders` or `order_items`. The worker fills the table on its first popularity refresh after restart.
- Recovery after a lock timeout: `./bin/migrate.new force <previous version>` (the version before the failing file), then re-run the deploy off-peak, as for 000040.

## Popularity refresh job (`product_sales`)
- The worker (`cmd/worker`) recomputes `product_sales` from `order_items JOIN orders` once at startup and then every `PRODUCT_SALES_REFRESH_SEC` seconds (default 300; `0` disables the job). Each tenant is refreshed in its own short transaction, guarded by a transaction-scoped advisory lock (`pg_try_advisory_xact_lock`), so several worker replicas never refresh the same tenant concurrently. A replica that finds the lock taken skips that tenant.
- Paid statuses that count: `PAID`, `PACKED`, `SHIPPED`, `OUT_FOR_DELIVERY`, `DELIVERED`. Cancelled or unpaid orders do not count, and rows for products with no paid sales are deleted.
- The payment webhook never touches `product_sales`. This is deliberate: a counter row updated inside the settlement transaction would serialise every paid webhook for a popular product and could exhaust the API's DB pool (see `internal/payment/webhook_concurrency_integration_test.go`).
- Signal: the worker logs `job=product_sales_refresh` with `tenants`, `skipped_locked`, `upserted`, `deleted` and `duration` on every run, or `popularity refresh failed` at error level.
- Manual repair: `product_sales` is derived data. Wait for the next run or restart the worker; `TRUNCATE product_sales;` followed by a worker restart rebuilds it from scratch. Never edit its rows by hand.

## Search scaling limits (`GET /api/v1/products?q=`)
- `q` is capped at 200 characters (400 above). The trigram fuzzy fallback and suggestions run only for 3-64 characters, because their cost grows with `len(q)` (measured on 20k products: a 2,000-character `q` took 4.7 s for the fuzzy count and the same again for suggestions).
- The fuzzy pass (count, list, facets, suggestions) runs in one read-only transaction with `statement_timeout = 500ms` (`catalog.DefaultFuzzyStatementTimeout`). When it fires, the API logs `catalog fuzzy search timed out; returning full-text result` at warn level and returns the empty full-text result; the request does not fail. Frequent occurrences mean the catalog has outgrown the cold path below.
- Suggestions (`meta.suggestions`) run only after both full-text and fuzzy search found nothing. For junk input the trigram index answers in under 1 ms. For a query whose words share trigrams with many titles (for example a 3-word typo of common words) Postgres scans the tenant's titles: about 100-120 ms on a 20k-product tenant, growing linearly. Every zero-result search of 3-64 characters can pay this. If a tenant grows far beyond that, consider a dedicated suggestions table or caching suggestions per normalised `q`.
- Broad full-text terms are served by the stored `search_vector_simple` column (see the 000049 section); at 20k products the worst case measured was ~42 ms for the list query and ~21 ms per count/facet query.
