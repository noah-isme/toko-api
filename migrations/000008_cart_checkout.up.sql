CREATE TABLE vouchers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  code TEXT UNIQUE NOT NULL,
  value BIGINT NOT NULL,
  min_spend BIGINT NOT NULL DEFAULT 0,
  usage_limit INT,
  used_count INT NOT NULL DEFAULT 0,
  valid_from TIMESTAMPTZ,
  valid_to TIMESTAMPTZ,
  product_ids UUID[] DEFAULT '{}',
  category_ids UUID[] DEFAULT '{}',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE carts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID,
  anon_id TEXT,
  applied_voucher_code TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ
);
CREATE INDEX idx_carts_user ON carts(user_id);
CREATE INDEX idx_carts_anon ON carts(anon_id);

CREATE TABLE cart_items (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  cart_id UUID NOT NULL REFERENCES carts(id) ON DELETE CASCADE,
  product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  variant_id UUID REFERENCES product_variants(id) ON DELETE SET NULL,
  title TEXT NOT NULL,
  slug TEXT NOT NULL,
  qty INT NOT NULL CHECK (qty>0),
  unit_price BIGINT NOT NULL,
  subtotal BIGINT NOT NULL
);
CREATE INDEX idx_cart_items_cart ON cart_items(cart_id);

CREATE TYPE order_status AS ENUM ('PENDING_PAYMENT','PAID','PACKED','SHIPPED','DELIVERED','CANCELED');
CREATE TYPE payment_status AS ENUM ('PENDING','PAID','FAILED','EXPIRED','REFUNDED');
CREATE TYPE shipment_status AS ENUM ('PENDING','PICKED','IN_TRANSIT','OUT_FOR_DELIVERY','DELIVERED','RETURNED');

CREATE TABLE orders (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID,
  cart_id UUID REFERENCES carts(id) ON DELETE SET NULL,
  status order_status NOT NULL DEFAULT 'PENDING_PAYMENT',
  currency TEXT NOT NULL DEFAULT 'IDR',
  pricing_subtotal BIGINT NOT NULL,
  pricing_discount BIGINT NOT NULL DEFAULT 0,
  pricing_tax BIGINT NOT NULL DEFAULT 0,
  pricing_shipping BIGINT NOT NULL DEFAULT 0,
  pricing_total BIGINT NOT NULL,
  shipping_address JSONB,
  shipping_option JSONB,
  notes TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE order_items (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  product_id UUID NOT NULL,
  variant_id UUID,
  title TEXT NOT NULL,
  slug TEXT NOT NULL,
  qty INT NOT NULL,
  unit_price BIGINT NOT NULL,
  subtotal BIGINT NOT NULL
);
CREATE INDEX idx_order_items_order ON order_items(order_id);

CREATE TABLE payments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  provider TEXT,
  status payment_status NOT NULL DEFAULT 'PENDING',
  provider_payload JSONB,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE shipments (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id UUID NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  status shipment_status NOT NULL DEFAULT 'PENDING',
  courier TEXT,
  tracking_number TEXT,
  history JSONB DEFAULT '[]'::jsonb
);
