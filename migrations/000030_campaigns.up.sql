BEGIN;

CREATE TABLE IF NOT EXISTS flash_sale_campaigns (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  slug TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'DRAFT' CHECK (status IN ('DRAFT', 'SCHEDULED', 'ACTIVE', 'ENDED')),
  starts_at TIMESTAMPTZ NOT NULL,
  ends_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (ends_at > starts_at),
  UNIQUE (tenant_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_flash_sale_campaigns_public
  ON flash_sale_campaigns(tenant_id, status, starts_at, ends_at);

CREATE TABLE IF NOT EXISTS flash_sale_items (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  campaign_id UUID NOT NULL REFERENCES flash_sale_campaigns(id) ON DELETE CASCADE,
  product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
  sale_price BIGINT NOT NULL CHECK (sale_price >= 0),
  stock_limit INT CHECK (stock_limit IS NULL OR stock_limit > 0),
  sold_count INT NOT NULL DEFAULT 0 CHECK (sold_count >= 0),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (campaign_id, product_id),
  CHECK (stock_limit IS NULL OR sold_count <= stock_limit)
);

CREATE INDEX IF NOT EXISTS idx_flash_sale_items_campaign
  ON flash_sale_items(campaign_id);

COMMIT;
