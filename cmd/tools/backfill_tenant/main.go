package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/noah-isme/backend-toko/internal/config"
)

func main() {
	var (
		tenantSlug   = flag.String("tenant", "default", "tenant slug to backfill")
		tenantName   = flag.String("tenant-name", "Default Tenant", "tenant name to ensure exists")
		tenantStatus = flag.String("tenant-status", "active", "status to set for the tenant record")
		tablesList   = flag.String("tables", "", "comma separated list of tables to update; defaults to all tables with tenant_id column")
		dryRun       = flag.Bool("dry-run", false, "print the operations without mutating data")
	)
	flag.Parse()

	baseCtx := context.Background()
	connectCtx, cancel := context.WithTimeout(baseCtx, 10*time.Second)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if strings.TrimSpace(cfg.DatabaseURL) == "" {
		log.Fatal("DATABASE_URL is required")
	}

	pool, err := pgxpool.New(connectCtx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer pool.Close()

	if err := pool.Ping(connectCtx); err != nil {
		log.Fatalf("ping database: %v", err)
	}

	tenantID, err := ensureTenant(baseCtx, pool, *tenantSlug, *tenantName, *tenantStatus, *dryRun)
	if err != nil {
		log.Fatalf("ensure tenant: %v", err)
	}

	tables, err := resolveTables(baseCtx, pool, *tablesList)
	if err != nil {
		log.Fatalf("resolve tables: %v", err)
	}

	if len(tables) == 0 {
		log.Println("no tables with tenant_id column found; nothing to do")
		return
	}

	if *dryRun {
		log.Printf("would update %d tables with tenant %s (%s)\n", len(tables), tenantID, *tenantSlug)
		for _, tbl := range tables {
			log.Printf("would backfill table %s.%s\n", tbl.Schema, tbl.Name)
		}
		return
	}

	for _, tbl := range tables {
		if err := backfillTable(baseCtx, pool, tbl, tenantID); err != nil {
			log.Fatalf("backfill %s.%s: %v", tbl.Schema, tbl.Name, err)
		}
		log.Printf("backfilled %s.%s", tbl.Schema, tbl.Name)
	}
}

type tableRef struct {
	Schema string
	Name   string
}

func ensureTenant(ctx context.Context, pool *pgxpool.Pool, slug, name, status string, dryRun bool) (string, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "", errors.New("tenant slug cannot be empty")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.ToUpper(slug)
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "active"
	}

	if dryRun {
		return fmt.Sprintf("dry-run-%s", slug), nil
	}

	var tenantID string
	err := pool.QueryRow(ctx,
		`INSERT INTO tenants (slug, name, status)
         VALUES ($1, $2, $3)
         ON CONFLICT (slug) DO UPDATE SET name = EXCLUDED.name, status = EXCLUDED.status
         RETURNING id`, slug, name, status,
	).Scan(&tenantID)
	if err != nil {
		return "", err
	}
	return tenantID, nil
}

func resolveTables(ctx context.Context, pool *pgxpool.Pool, tablesFlag string) ([]tableRef, error) {
	if strings.TrimSpace(tablesFlag) != "" {
		parts := strings.Split(tablesFlag, ",")
		tables := make([]tableRef, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			schema := "public"
			name := part
			if strings.Contains(part, ".") {
				items := strings.SplitN(part, ".", 2)
				schema = strings.TrimSpace(items[0])
				name = strings.TrimSpace(items[1])
			}
			if schema == "" || name == "" {
				return nil, fmt.Errorf("invalid table reference: %q", part)
			}
			tables = append(tables, tableRef{Schema: schema, Name: name})
		}
		sortTables(tables)
		return tables, nil
	}

	rows, err := pool.Query(ctx, `
        SELECT table_schema, table_name
        FROM information_schema.columns
        WHERE column_name = 'tenant_id'
          AND table_schema NOT IN ('pg_catalog', 'information_schema')
        GROUP BY table_schema, table_name
    `)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []tableRef
	for rows.Next() {
		var tr tableRef
		if err := rows.Scan(&tr.Schema, &tr.Name); err != nil {
			return nil, err
		}
		tables = append(tables, tr)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	sortTables(tables)
	return tables, nil
}

func sortTables(tables []tableRef) {
	sort.Slice(tables, func(i, j int) bool {
		if tables[i].Schema == tables[j].Schema {
			return tables[i].Name < tables[j].Name
		}
		return tables[i].Schema < tables[j].Schema
	})
}

func backfillTable(ctx context.Context, pool *pgxpool.Pool, tbl tableRef, tenantID string) error {
	identifier := pgx.Identifier{tbl.Schema, tbl.Name}.Sanitize()
	updateSQL := fmt.Sprintf("UPDATE %s SET tenant_id = $1 WHERE tenant_id IS NULL", identifier)
	alterSQL := fmt.Sprintf("ALTER TABLE %s ALTER COLUMN tenant_id SET DEFAULT $1", identifier)

	if _, err := pool.Exec(ctx, alterSQL, tenantID); err != nil {
		return fmt.Errorf("set default: %w", err)
	}
	if _, err := pool.Exec(ctx, updateSQL, tenantID); err != nil {
		return fmt.Errorf("update rows: %w", err)
	}
	return nil
}
