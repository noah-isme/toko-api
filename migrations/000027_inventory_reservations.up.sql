CREATE TYPE inventory_reservation_status AS ENUM ('RESERVED', 'COMMITTED', 'RELEASED', 'EXPIRED');

CREATE TABLE inventory_reservations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  product_id UUID NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
  variant_id UUID NOT NULL REFERENCES product_variants(id) ON DELETE RESTRICT,
  qty INT NOT NULL CHECK (qty > 0),
  status inventory_reservation_status NOT NULL DEFAULT 'RESERVED',
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (order_id, variant_id)
);

CREATE INDEX idx_inventory_reservations_expiry ON inventory_reservations(status, expires_at);
CREATE INDEX idx_inventory_reservations_tenant ON inventory_reservations(tenant_id, created_at DESC);
