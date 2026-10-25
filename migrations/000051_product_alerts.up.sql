SET LOCAL lock_timeout = '5s';

-- Back-in-stock / price-drop alert subscriptions. One row per
-- (tenant, user, product, variant-or-none, type); re-subscribing re-arms the
-- row (notified_at = NULL) instead of inserting a duplicate.
CREATE TABLE IF NOT EXISTS product_alert_subscriptions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    variant_id UUID NULL REFERENCES product_variants(id) ON DELETE CASCADE,
    alert_type TEXT NOT NULL CHECK (alert_type IN ('BACK_IN_STOCK', 'PRICE_DROP')),
    price_at_subscribe BIGINT NULL,
    notified_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT product_alert_subscriptions_price_check
        CHECK (alert_type <> 'PRICE_DROP' OR price_at_subscribe IS NOT NULL)
);

CREATE UNIQUE INDEX IF NOT EXISTS ux_product_alert_subscriptions_target
    ON product_alert_subscriptions (
        tenant_id,
        user_id,
        product_id,
        COALESCE(variant_id, '00000000-0000-0000-0000-000000000000'::uuid),
        alert_type
    );

CREATE INDEX IF NOT EXISTS idx_product_alert_subscriptions_pending
    ON product_alert_subscriptions (tenant_id, product_id, alert_type, created_at, id)
    WHERE notified_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_product_alert_subscriptions_user
    ON product_alert_subscriptions (user_id, tenant_id, created_at DESC);

-- Outbox of stock/price transitions, written by the triggers below and drained
-- by the API's background alerts runner.
CREATE TABLE IF NOT EXISTS product_alert_events (
    id BIGSERIAL PRIMARY KEY,
    tenant_id UUID NOT NULL,
    product_id UUID NOT NULL,
    variant_id UUID NULL,
    alert_type TEXT NOT NULL CHECK (alert_type IN ('BACK_IN_STOCK', 'PRICE_DROP')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    processed_at TIMESTAMPTZ NULL,
    -- Set when a drain pass left the event pending because it had more
    -- claimable subscriptions than that pass's per-event share. Pending events
    -- are drained in (last_attempt_at NULLS FIRST, id) order, so a popular
    -- product rotates to the back instead of starving every later event.
    last_attempt_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_product_alert_events_pending
    ON product_alert_events (last_attempt_at NULLS FIRST, id)
    WHERE processed_at IS NULL;

-- Retention: processed rows are pruned by the drainer after a grace period.
CREATE INDEX IF NOT EXISTS idx_product_alert_events_processed
    ON product_alert_events (processed_at)
    WHERE processed_at IS NOT NULL;

-- Durable email outbox for alerts. The drainer inserts one row per alert
-- domain_events row in the same transaction that claims the subscription;
-- the alerts mailer leases due rows, hands the domain event to the existing
-- notifiers (Bus.Deliver), and records the outcome with retry/backoff.
CREATE TABLE IF NOT EXISTS product_alert_deliveries (
    event_id UUID PRIMARY KEY REFERENCES domain_events(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    attempts INT NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at TIMESTAMPTZ NULL,
    failed_at TIMESTAMPTZ NULL,
    last_error TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_product_alert_deliveries_due
    ON product_alert_deliveries (next_attempt_at, event_id)
    WHERE delivered_at IS NULL AND failed_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_product_alert_deliveries_tenant
    ON product_alert_deliveries (tenant_id);

CREATE OR REPLACE FUNCTION product_alert_variant_changed() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
    v_tenant UUID;
BEGIN
    SELECT p.tenant_id INTO v_tenant FROM products p WHERE p.id = NEW.product_id;
    IF v_tenant IS NULL THEN
        RETURN NULL;
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.stock > 0 THEN
            INSERT INTO product_alert_events (tenant_id, product_id, variant_id, alert_type)
            VALUES (v_tenant, NEW.product_id, NEW.id, 'BACK_IN_STOCK');
        END IF;
        RETURN NULL;
    END IF;
    IF OLD.stock <= 0 AND NEW.stock > 0 THEN
        INSERT INTO product_alert_events (tenant_id, product_id, variant_id, alert_type)
        VALUES (v_tenant, NEW.product_id, NEW.id, 'BACK_IN_STOCK');
    END IF;
    IF NEW.price < OLD.price THEN
        INSERT INTO product_alert_events (tenant_id, product_id, variant_id, alert_type)
        VALUES (v_tenant, NEW.product_id, NEW.id, 'PRICE_DROP');
    END IF;
    RETURN NULL;
END;
$$;

CREATE OR REPLACE FUNCTION product_alert_product_changed() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF NOT OLD.in_stock AND NEW.in_stock THEN
        INSERT INTO product_alert_events (tenant_id, product_id, variant_id, alert_type)
        VALUES (NEW.tenant_id, NEW.id, NULL, 'BACK_IN_STOCK');
    END IF;
    IF NEW.price < OLD.price THEN
        INSERT INTO product_alert_events (tenant_id, product_id, variant_id, alert_type)
        VALUES (NEW.tenant_id, NEW.id, NULL, 'PRICE_DROP');
    END IF;
    RETURN NULL;
END;
$$;

-- WHEN clauses keep the hot stock-reservation path (ReserveVariantStock) from
-- even entering plpgsql unless a relevant transition happened.
DROP TRIGGER IF EXISTS trg_product_alert_variant_insert ON product_variants;
CREATE TRIGGER trg_product_alert_variant_insert
    AFTER INSERT ON product_variants
    FOR EACH ROW
    WHEN (NEW.stock > 0)
    EXECUTE FUNCTION product_alert_variant_changed();

DROP TRIGGER IF EXISTS trg_product_alert_variant_update ON product_variants;
CREATE TRIGGER trg_product_alert_variant_update
    AFTER UPDATE OF stock, price ON product_variants
    FOR EACH ROW
    WHEN ((OLD.stock <= 0 AND NEW.stock > 0) OR NEW.price < OLD.price)
    EXECUTE FUNCTION product_alert_variant_changed();

DROP TRIGGER IF EXISTS trg_product_alert_product_update ON products;
CREATE TRIGGER trg_product_alert_product_update
    AFTER UPDATE OF in_stock, price ON products
    FOR EACH ROW
    WHEN ((NOT OLD.in_stock AND NEW.in_stock) OR NEW.price < OLD.price)
    EXECUTE FUNCTION product_alert_product_changed();
