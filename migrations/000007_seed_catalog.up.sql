INSERT INTO brands (name, slug)
VALUES ('Acme', 'acme')
ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name;

INSERT INTO categories (name, slug)
VALUES ('fashion', 'fashion')
ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name;

WITH b AS (
  SELECT id FROM brands WHERE slug = 'acme'
), c AS (
  SELECT id FROM categories WHERE slug = 'fashion'
)
INSERT INTO products (title, slug, brand_id, category_id, price, compare_at, in_stock, thumbnail, badges)
SELECT 'Kaos Hitam', 'kaos-hitam', b.id, c.id, 249000, 299000, true, 'https://cdn.example/kaos.jpg', ARRAY['promo','new']
FROM b, c
ON CONFLICT (slug) DO UPDATE SET
  title = EXCLUDED.title,
  brand_id = EXCLUDED.brand_id,
  category_id = EXCLUDED.category_id,
  price = EXCLUDED.price,
  compare_at = EXCLUDED.compare_at,
  in_stock = EXCLUDED.in_stock,
  thumbnail = EXCLUDED.thumbnail,
  badges = EXCLUDED.badges;
