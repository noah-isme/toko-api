CREATE TABLE flash_sale_order_items (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  flash_sale_item_id UUID NOT NULL REFERENCES flash_sale_items(id) ON DELETE RESTRICT,
  qty INT NOT NULL CHECK (qty > 0),
  status TEXT NOT NULL DEFAULT 'RESERVED' CHECK (status IN ('RESERVED', 'COMMITTED', 'RELEASED')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (order_id, flash_sale_item_id)
);

CREATE INDEX idx_flash_sale_order_items_order_status
  ON flash_sale_order_items(order_id, status);
