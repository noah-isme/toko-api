package toko

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// tableColumns returns the columns the migrations define for a table, taken
// from its CREATE TABLE body plus any ALTER TABLE ... ADD COLUMN statements.
func tableColumns(t *testing.T, table string) map[string]bool {
	t.Helper()
	entries, err := Migrations.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	create := regexp.MustCompile(`(?is)CREATE TABLE(?: IF NOT EXISTS)?\s+` + table + `\s*\((.*?)\n\);`)
	alter := regexp.MustCompile(`(?i)ALTER TABLE(?: IF EXISTS)?\s+` + table + `\s+ADD COLUMN(?: IF NOT EXISTS)?\s+([a-z_][a-z0-9_]*)`)
	constraintStart := regexp.MustCompile(`(?i)^(PRIMARY|UNIQUE|CHECK|FOREIGN|CONSTRAINT)\b`)

	cols := map[string]bool{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".up.sql") {
			continue
		}
		body := readMigration(t, "migrations/"+entry.Name())
		for _, m := range create.FindAllStringSubmatch(body, -1) {
			for _, line := range strings.Split(m[1], "\n") {
				line = strings.TrimSpace(line)
				if line == "" || constraintStart.MatchString(line) {
					continue
				}
				if name := strings.Fields(line)[0]; regexp.MustCompile(`^[a-z_][a-z0-9_]*$`).MatchString(name) {
					cols[name] = true
				}
			}
		}
		for _, m := range alter.FindAllStringSubmatch(body, -1) {
			cols[m[1]] = true
		}
	}
	return cols
}

// TestFlashSaleItemsQueriesOnlyAssignExistingColumns guards against sqlc
// queries that SET a flash_sale_items column the table does not have. Such a
// query generates fine but fails at runtime with "column does not exist", which
// is how ReserveFlashSaleItem once broke flash-sale reservation at checkout.
func TestFlashSaleItemsQueriesOnlyAssignExistingColumns(t *testing.T) {
	cols := tableColumns(t, "flash_sale_items")
	for _, want := range []string{"id", "campaign_id", "product_id", "sale_price", "stock_limit", "sold_count"} {
		if !cols[want] {
			t.Fatalf("migration parser did not find flash_sale_items.%s (found %v)", want, cols)
		}
	}

	files, err := filepath.Glob("internal/db/queries/*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("no query files found: %v", err)
	}

	update := regexp.MustCompile(`(?is)UPDATE\s+flash_sale_items\b(?:\s+(?:AS\s+)?[a-z_]+)?\s+SET\s+(.*?)(?:\bFROM\b|\bWHERE\b|\bRETURNING\b|;)`)
	assign := regexp.MustCompile(`(?i)(?:^|,)\s*(?:[a-z_]+\.)?([a-z_][a-z0-9_]*)\s*=`)

	checked := 0
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, m := range update.FindAllStringSubmatch(string(raw), -1) {
			checked++
			// Drop parenthesised sub-expressions so commas inside them do not
			// look like further assignments.
			set := m[1]
			for strings.Contains(set, "(") {
				set = regexp.MustCompile(`\([^()]*\)`).ReplaceAllString(set, "_")
			}
			for _, a := range assign.FindAllStringSubmatch(set, -1) {
				if !cols[a[1]] {
					t.Errorf("%s: UPDATE flash_sale_items sets %q, which no migration defines", file, a[1])
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("expected to find UPDATE flash_sale_items statements to check")
	}
}
