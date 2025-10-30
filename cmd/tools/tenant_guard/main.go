package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

var (
	reSelect = regexp.MustCompile(`(?i)^\s*select\b`)
	reUpdate = regexp.MustCompile(`(?i)^\s*update\b`)
	reDelete = regexp.MustCompile(`(?i)^\s*delete\b`)
	reTenant = regexp.MustCompile(`(?i)tenant_id\s*=\s*\$?[0-9a-z_]+`)
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
	defer func() {
		_ = f.Close()
	}()
	s := bufio.NewScanner(f)
	foundStmt := false
	foundTenant := false
	for s.Scan() {
		line := s.Text()
		if reSelect.MatchString(line) || reUpdate.MatchString(line) || reDelete.MatchString(line) {
			foundStmt = true
		}
		if reTenant.MatchString(line) {
			foundTenant = true
		}
	}
	if err := s.Err(); err != nil {
		return false, err
	}
	if !foundStmt {
		return true, nil
	}
	if !foundTenant {
		return false, nil
	}
	return true, nil
}
