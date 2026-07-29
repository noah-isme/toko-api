// Package toko tests guard the embedded migration set. The seed migration is
// generated (scripts/seed/gen_seed_sql.py), so these checks catch a stale or
// hand-edited file before it reaches a database.
package toko

import (
	"regexp"
	"strings"
	"testing"
)

const seedUp = "migrations/000025_full_seed_catalog.up.sql"
const seedDown = "migrations/000025_full_seed_catalog.down.sql"

func readMigration(t *testing.T, name string) string {
	t.Helper()
	body, err := Migrations.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

func TestMigrationsEmbedUpDownPairs(t *testing.T) {
	entries, err := Migrations.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	versions := map[string]map[string]bool{}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			version := strings.TrimSuffix(name, ".up.sql")
			if versions[version] == nil {
				versions[version] = map[string]bool{}
			}
			versions[version]["up"] = true
		case strings.HasSuffix(name, ".down.sql"):
			version := strings.TrimSuffix(name, ".down.sql")
			if versions[version] == nil {
				versions[version] = map[string]bool{}
			}
			versions[version]["down"] = true
		default:
			t.Errorf("unexpected migration file %q", name)
		}
	}

	for version, kinds := range versions {
		if !kinds["up"] || !kinds["down"] {
			t.Errorf("migration %s missing a direction: %v", version, kinds)
		}
	}
}

func TestSeedCatalogMigrationIsTransactional(t *testing.T) {
	for _, name := range []string{seedUp, seedDown} {
		body := readMigration(t, name)
		if !strings.Contains(body, "BEGIN;") || !strings.Contains(body, "COMMIT;") {
			t.Errorf("%s must wrap its statements in BEGIN/COMMIT", name)
		}
	}
}

func TestSeedCatalogUpsertsInsteadOfFailing(t *testing.T) {
	body := readMigration(t, seedUp)
	// Re-running the seed must refresh rows rather than abort on a unique
	// violation, otherwise a redeploy against a seeded database breaks.
	inserts := strings.Count(body, "INSERT INTO")
	conflicts := strings.Count(body, "ON CONFLICT")
	if conflicts < 3 {
		t.Errorf("expected upserts for brands, categories and products, got %d ON CONFLICT clauses in %d inserts", conflicts, inserts)
	}
	for _, table := range []string{"product_variants", "product_images", "product_specs"} {
		if !strings.Contains(body, "DELETE FROM "+table+" WHERE product_id IN (") {
			t.Errorf("%s: child rows in %s must be cleared before reinsert to stay idempotent", seedUp, table)
		}
	}
}

func TestSeedCatalogCoversCatalogShape(t *testing.T) {
	body := readMigration(t, seedUp)

	slugs := regexp.MustCompile(`INSERT INTO products \(`)
	if !slugs.MatchString(body) {
		t.Fatalf("%s does not insert products", seedUp)
	}

	// The storefront filters by category and brand, so the seed must populate
	// every category the frontend mock catalog offers.
	for _, category := range []string{
		"electronics", "fashion", "home-living", "beauty", "sports",
		"toys", "books", "automotive", "health", "garden",
	} {
		if !strings.Contains(body, "categories WHERE slug = '"+category+"'") {
			t.Errorf("no product assigned to category %q", category)
		}
	}

	if count := strings.Count(body, "images.unsplash.com/photo-"); count < 100 {
		t.Errorf("expected multiple images per product, found %d image urls", count)
	}
	if !strings.Contains(body, "INSERT INTO product_specs") {
		t.Errorf("%s must seed product specs", seedUp)
	}
	if !strings.Contains(body, "INSERT INTO product_variants") {
		t.Errorf("%s must seed product variants", seedUp)
	}
}

func TestSeedCatalogSkusAreUnique(t *testing.T) {
	body := readMigration(t, seedUp)
	sku := regexp.MustCompile(`'(TK-[A-Z0-9]+-[A-Z0-9-]+)'`)
	seen := map[string]bool{}
	for _, match := range sku.FindAllStringSubmatch(body, -1) {
		if seen[match[1]] {
			t.Errorf("duplicate sku %s would violate product_variants.sku unique index", match[1])
		}
		seen[match[1]] = true
	}
	if len(seen) < 50 {
		t.Errorf("expected variants across the catalog, found %d skus", len(seen))
	}
}

func TestSeedCatalogProductSlugsAreUnique(t *testing.T) {
	body := readMigration(t, seedUp)
	// Product tuples start with the title then the slug, both single quoted.
	slug := regexp.MustCompile(`products WHERE slug IN \(([^)]+)\)`)
	match := slug.FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("%s does not scope child deletes by product slug", seedUp)
	}
	seen := map[string]bool{}
	for _, raw := range strings.Split(match[1], ",") {
		value := strings.Trim(strings.TrimSpace(raw), "'")
		if value == "" {
			continue
		}
		if seen[value] {
			t.Errorf("duplicate product slug %s", value)
		}
		seen[value] = true
	}
	if len(seen) < 50 {
		t.Errorf("expected 50+ seeded products, found %d", len(seen))
	}
}

func TestSeedCatalogDownOnlyRemovesSeededRows(t *testing.T) {
	body := readMigration(t, seedDown)
	if strings.Contains(body, "TRUNCATE") {
		t.Errorf("%s must not truncate shared catalog tables", seedDown)
	}
	if !strings.Contains(body, "DELETE FROM products WHERE slug IN (") {
		t.Errorf("%s must delete products by seeded slug", seedDown)
	}
	// Brands and categories are shared with migration 000007 and with any rows a
	// user created, so the rollback has to leave referenced ones alone.
	if !strings.Contains(body, "NOT EXISTS (SELECT 1 FROM products p WHERE p.category_id = c.id)") {
		t.Errorf("%s must keep categories that still have products", seedDown)
	}
	if !strings.Contains(body, "NOT EXISTS (SELECT 1 FROM products p WHERE p.brand_id = b.id)") {
		t.Errorf("%s must keep brands that still have products", seedDown)
	}
}
