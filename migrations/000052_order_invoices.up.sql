SET LOCAL lock_timeout = '5s';

-- Per-tenant invoice numbering. Kept out of the orders table on purpose: no
-- ALTER on orders (no ACCESS EXCLUSIVE lock during deploy) and the generated
-- dbgen.Order stays unchanged.
CREATE TABLE IF NOT EXISTS invoice_counters (
    tenant_id UUID PRIMARY KEY REFERENCES tenants(id) ON DELETE CASCADE,
    last_number BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per invoiced order; the number is assigned once and never changes.
CREATE TABLE IF NOT EXISTS order_invoices (
    order_id UUID PRIMARY KEY REFERENCES orders(id) ON DELETE CASCADE,
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    invoice_number TEXT NOT NULL,
    issued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT ux_order_invoices_tenant_number UNIQUE (tenant_id, invoice_number)
);
