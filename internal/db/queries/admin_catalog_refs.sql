-- Ownership checks for references written by the admin catalog API.
-- products.brand_id / products.category_id are plain FKs, so without these
-- checks a product could point at another tenant's brand or category.

-- name: AdminBrandBelongsToTenant :one
SELECT EXISTS (
    SELECT 1 FROM brands
    WHERE id = sqlc.arg(id)
      AND tenant_id = sqlc.arg(tenant_id)
) AS ok;

-- name: AdminCategoryBelongsToTenant :one
SELECT EXISTS (
    SELECT 1 FROM categories
    WHERE id = sqlc.arg(id)
      AND tenant_id = sqlc.arg(tenant_id)
) AS ok;
