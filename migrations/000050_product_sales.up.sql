SET LOCAL lock_timeout = '5s';
-- product_sales: derived per-product counter of units sold in paid orders, used by the
-- storefront popularity sort. It is written ONLY by the worker's periodic refresh job
-- (internal/catalog/popularity.go), which recomputes it per tenant from order_items JOIN
-- orders; popularity may therefore be up to one refresh interval stale. Deliberately no
-- triggers: keeping the counter out of the payment-settlement transaction means paid
-- webhooks never contend on a shared row (see .plans/rec-api-search.md, Decision 5/15).
-- Lock footprint: the FK takes a brief SHARE ROW EXCLUSIVE on products; no backfill here
-- (the first worker run fills the table), no lock on orders/order_items.
CREATE TABLE IF NOT EXISTS product_sales (
  product_id UUID PRIMARY KEY REFERENCES products(id) ON DELETE CASCADE,
  tenant_id  UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  units_sold BIGINT NOT NULL DEFAULT 0 CHECK (units_sold >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_product_sales_tenant ON product_sales(tenant_id);
