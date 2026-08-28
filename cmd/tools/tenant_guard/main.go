package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// tenantGuard scans admin SQL query files and ensures SELECT/UPDATE/DELETE
// against required tenant-bearing tables contain a tenant_id filter.
// Exit code 0 = ok, 1 = violation, 2 = other error.
func main() {
	root := "internal/db/queries"
	deny, err := scan(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tenant_guard error: %v\n", err)
		os.Exit(2)
	}
	if len(deny) > 0 {
		for _, v := range deny {
			fmt.Fprintf(os.Stderr, "VIOLATION: %s\n", v)
		}
		os.Exit(1)
	}
	fmt.Println("tenant_guard: OK")
}

// Tables that DON'T require tenant_id in queries (no tenant_id column, or implicitly scoped)
var exemptTables = map[string]bool{
	// Global reference tables (no tenant_id column)
	"brands":          true,
	"categories":      true,
	"users":           true,
	"audit_logs":      true,
	"password_resets": true,
	"sessions":        true,
	"domain_events":   true, // filtered by topic/aggregate_id
	"webhook_dlq":     true, // scoped by delivery
	// Implicitly tenant-scoped via cart_id (cart has tenant_id)
	"cart_items": true,
	// Implicitly tenant-scoped via user_id (users -> tenant mapping exists)
	"addresses": true,
	// Materialized views (no direct table access)
	"mv_sales_daily":  true,
	"mv_top_products": true,
	// Webhook tables (scoped by endpoint which has tenant_id)
	"webhook_endpoints":  true,
	"webhook_deliveries": true,
	// Tables with globally unique UUID primary keys - implicitly tenant-scoped
	// These use globally unique UUID PKs; tenant isolation is enforced at app layer
	"carts":             true,
	"orders":            true,
	"payments":          true,
	"shipments":         true,
	"notifications":     true,
	"reviews":           true,
	"favorites":         true,
	"vouchers":          true,
	"voucher_rules":     true,
	"orders_status":     true,
	"orders_settlement": true,
}

// Tables that MUST have tenant_id in admin SELECT/UPDATE/DELETE queries. The
// guard is intentionally limited to admin_*.sql: the remaining query files
// include system jobs and parent-key-scoped operations that need a more
// complete SQL parser and table inventory before they can be checked safely.
var requiresTenantIDTables = map[string]bool{
	"brands":     true,
	"categories": true,
	"carts":      true,
	"orders":     true,
	"products":   true,
	"vouchers":   true,
}

var (
	reSelect   = regexp.MustCompile(`(?i)^\s*select\b`)
	reUpdate   = regexp.MustCompile(`(?i)^\s*update\b`)
	reDelete   = regexp.MustCompile(`(?i)^\s*delete\b`)
	reTenant   = regexp.MustCompile(`(?i)\b(?:\w+\.)?tenant_id\s*=\s*(?:\$[0-9]+|sqlc\.(?:n?arg)\([^)]*\)|(?:\w+\.)?tenant_id|\w+)`)
	reTableRef = regexp.MustCompile(`(?i)\b(from|update|delete\s+from|join)\s+(\w+)`)
)

func scan(dir string) ([]string, error) {
	var violations []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(path) != ".sql" {
			return nil
		}
		ok, err := checkFile(path)
		if err != nil {
			return err
		}
		if !ok {
			violations = append(violations, path)
		}
		return nil
	})
	return violations, err
}

func checkFile(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()

	contents, err := io.ReadAll(f)
	if err != nil {
		return false, err
	}
	if !isAdminQueryFile(path) {
		return true, nil
	}

	violations := 0
	for _, rawStatement := range strings.Split(string(contents), ";") {
		statement := stripSQLComments(rawStatement)
		if statement == "" {
			continue
		}
		if reSelect.MatchString(statement) || reUpdate.MatchString(statement) || reDelete.MatchString(statement) {
			// Check if this query references a table that requires tenant_id
			if requiresTenantID(statement) && !hasTenantID(statement) {
				violations++
			}
		}
	}
	return violations == 0, nil
}

func isAdminQueryFile(path string) bool {
	return strings.HasPrefix(strings.ToLower(filepath.Base(path)), "admin_")
}

// stripSQLComments removes line comments before statement matching. Query
// files use comments for sqlc names and documentation; removing them lets a
// query start on any line without weakening the statement boundary check.
func stripSQLComments(sql string) string {
	var b strings.Builder
	for _, line := range strings.Split(sql, "\n") {
		if idx := strings.Index(line, "--"); idx >= 0 {
			line = line[:idx]
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

// requiresTenantID checks if the query references a table that requires tenant_id filtering
func requiresTenantID(query string) bool {
	matches := reTableRef.FindAllStringSubmatch(query, -1)
	for _, m := range matches {
		if len(m) >= 3 {
			table := strings.ToLower(m[2])
			// Check if this table requires tenant_id
			if requiresTenantIDTables[table] {
				return true
			}
			// Skip tables that are globally scoped or reached through a
			// tenant-bearing parent key.
			if exemptTables[table] {
				continue
			}
		}
	}
	return false
}

func hasTenantID(query string) bool {
	return reTenant.MatchString(query)
}
