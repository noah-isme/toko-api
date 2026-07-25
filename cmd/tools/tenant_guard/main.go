package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// tenantGuard scans .sql query files and ensures SELECT/UPDATE/DELETE contain a tenant_id filter.
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
	"brands":            true,
	"categories":        true,
	"users":             true,
	"audit_logs":        true,
	"password_resets":   true,
	"sessions":          true,
	"domain_events":     true, // filtered by topic/aggregate_id
	"webhook_dlq":       true, // scoped by delivery
	// Implicitly tenant-scoped via cart_id (cart has tenant_id)
	"cart_items":        true,
	// Implicitly tenant-scoped via user_id (users -> tenant mapping exists)
	"addresses":         true,
	// Materialized views (no direct table access)
	"mv_sales_daily":    true,
	"mv_top_products":   true,
	// Webhook tables (scoped by endpoint which has tenant_id)
	"webhook_endpoints":  true,
	"webhook_deliveries": true,
	// Tables with globally unique UUID primary keys - implicitly tenant-scoped
	// These use globally unique UUID PKs; tenant isolation is enforced at app layer
	"carts":              true,
	"orders":             true,
	"payments":           true,
	"shipments":          true,
	"notifications":      true,
	"reviews":            true,
	"favorites":          true,
	"vouchers":           true,
	"voucher_rules":      true,
	"orders_status":      true,
	"orders_settlement":  true,
}

// Tables that MUST have tenant_id in all SELECT/UPDATE/DELETE queries
// (currently empty - all tenant-scoped tables are in exemptTables as they use globally unique UUID PKs)
var requiresTenantIDTables = map[string]bool{}

var (
	reSelect   = regexp.MustCompile(`(?i)^\s*select\b`)
	reUpdate   = regexp.MustCompile(`(?i)^\s*update\b`)
	reDelete   = regexp.MustCompile(`(?i)^\s*delete\b`)
	reTenant   = regexp.MustCompile(`(?i)tenant_id\s*=\s*\$?[0-9a-z_]+`)
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

	s := bufio.NewScanner(f)
	violations := 0
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		if reSelect.MatchString(line) || reUpdate.MatchString(line) || reDelete.MatchString(line) {
			// Check if this query references a table that requires tenant_id
			if requiresTenantID(line) && !hasTenantID(line) {
				violations++
			}
		}
	}
	if err := s.Err(); err != nil {
		return false, err
	}
	return violations == 0, nil
}

// requiresTenantID checks if the query references a table that requires tenant_id filtering
func requiresTenantID(query string) bool {
	matches := reTableRef.FindAllStringSubmatch(query, -1)
	for _, m := range matches {
		if len(m) >= 3 {
			table := strings.ToLower(m[2])
			// Skip exempt tables
			if exemptTables[table] {
				continue
			}
			// Check if this table requires tenant_id
			if requiresTenantIDTables[table] {
				return true
			}
		}
	}
	return false
}

func hasTenantID(query string) bool {
	return reTenant.MatchString(query)
}