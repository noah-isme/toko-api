-- Migration for recommendation engine tracking tables
-- Tracks user product views, order product pairs for FBT, and recommendation cache

-- Table to track user product views for collaborative filtering
CREATE TABLE user_product_views (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    product_id UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    view_count INT NOT NULL DEFAULT 1,
    last_viewed_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_user_product_views_unique ON user_product_views(tenant_id, user_id, product_id);
CREATE INDEX idx_user_product_views_user ON user_product_views(tenant_id, user_id);
CREATE INDEX idx_user_product_views_product ON user_product_views(tenant_id, product_id);
CREATE INDEX idx_user_product_views_last_viewed ON user_product_views(last_viewed_at DESC);

-- Table to track product pairs from orders for Frequently Bought Together
CREATE TABLE order_product_pairs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    product_id_a UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    product_id_b UUID NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    pair_count INT NOT NULL DEFAULT 1,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_order_product_pairs_unique ON order_product_pairs(tenant_id, product_id_a, product_id_b) WHERE product_id_a < product_id_b;
CREATE INDEX idx_order_product_pairs_a ON order_product_pairs(tenant_id, product_id_a);
CREATE INDEX idx_order_product_pairs_b ON order_product_pairs(tenant_id, product_id_b);
CREATE INDEX idx_order_product_pairs_count ON order_product_pairs(pair_count DESC);

-- Table to cache personalized recommendations per user
CREATE TABLE user_recommendation_cache (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
    user_id UUID NOT NULL,
    recommendation_type TEXT NOT NULL CHECK (recommendation_type IN ('personalized', 'trending', 'fbt', 'also_viewed')),
    product_ids UUID[] NOT NULL DEFAULT '{}',
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX idx_user_recommendation_cache_unique ON user_recommendation_cache(tenant_id, user_id, recommendation_type);
CREATE INDEX idx_user_recommendation_cache_expires ON user_recommendation_cache(expires_at) WHERE expires_at > now();