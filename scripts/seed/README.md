# Catalog seed generator

`migrations/000025_full_seed_catalog.{up,down}.sql` is generated, not hand
written. Editing the SQL directly means the next regeneration silently discards
the edit, so change the Python inputs instead.

## Files

| File | Role |
| --- | --- |
| `catalog_data.py` | Source of truth: categories, brands, products, variants, specs, descriptions. |
| `fetch_unsplash_ids.py` | Resolves each product's search query to a real `images.unsplash.com/photo-<id>`. |
| `unsplash_ids.json` | Cached harvest output, consumed by the generator. |
| `queries.json` | Per-product image search queries. |
| `gen_seed_sql.py` | Renders the up and down migrations. |
| `verify_images.py` | Checks every image URL in the generated SQL returns 200. |

## Regenerating

```sh
cd scripts/seed
python3 fetch_unsplash_ids.py     # optional; only when image IDs need refreshing
python3 gen_seed_sql.py
python3 verify_images.py
cd ../.. && go test .
```

`fetch_unsplash_ids.py` needs a local SearXNG at `http://localhost:8080` (override
with `SEARX_URL`). It queries image search, keeps results already hosted on
`images.unsplash.com`, and HTTP-checks each candidate before accepting it, so
retired photos never reach the migration. A full pass takes a few minutes and
individual engines get suspended under load, hence the retry loop.

## Invariants

`gen_seed_sql.py` guarantees properties that `migrations_test.go` asserts:

- Brands, categories and products upsert on their natural key (slug), so
  re-applying the migration refreshes rows instead of failing.
- Variants, images and specs are deleted for the seeded product slugs before
  reinsertion, which keeps child rows idempotent rather than duplicated.
- Every product gets three images. If the harvest plus fallbacks cannot reach
  three, generation fails loudly instead of emitting a thin gallery.
- The down migration deletes only seeded slugs, and drops brands and categories
  only when no product still references them, so migration `000007`'s rows and
  any operator-created rows survive a rollback.
