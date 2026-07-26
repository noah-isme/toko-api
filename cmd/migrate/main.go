// Command migrate applies the embedded database migrations.
//
// It exists so a deployment can migrate itself: the runtime image carries no
// SQL files, and requiring operators to run the golang-migrate CLI out of band
// means the schema can silently lag behind the code that needs it.
//
// Usage:
//
//	migrate up            apply all pending migrations (default)
//	migrate down          roll back the most recent migration
//	migrate version       print the current version and dirty state
//	migrate force <ver>   clear a dirty state by pinning the version
package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	toko "github.com/noah-isme/backend-toko"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	command := "up"
	if len(args) > 0 {
		command = args[0]
	}

	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required")
	}

	source, err := iofs.New(toko.Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}

	migrator, err := migrate.NewWithSourceInstance("iofs", source, databaseURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer func() {
		// Both errors are reported, but neither should mask a migration failure.
		if sourceErr, dbErr := migrator.Close(); sourceErr != nil || dbErr != nil {
			fmt.Fprintf(os.Stderr, "migrate: close: source=%v db=%v\n", sourceErr, dbErr)
		}
	}()

	switch command {
	case "up":
		if err := migrator.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("up: %w", err)
		}
		return reportVersion(migrator, "migrations applied")
	case "down":
		if err := migrator.Steps(-1); err != nil && !errors.Is(err, migrate.ErrNoChange) {
			return fmt.Errorf("down: %w", err)
		}
		return reportVersion(migrator, "rolled back one migration")
	case "version":
		return reportVersion(migrator, "current schema")
	case "force":
		if len(args) < 2 {
			return errors.New("force requires a version argument")
		}
		version, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("force: invalid version %q: %w", args[1], err)
		}
		if err := migrator.Force(version); err != nil {
			return fmt.Errorf("force: %w", err)
		}
		return reportVersion(migrator, "forced schema")
	default:
		return fmt.Errorf("unknown command %q (want up, down, version or force)", command)
	}
}

func reportVersion(migrator *migrate.Migrate, label string) error {
	version, dirty, err := migrator.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		fmt.Printf("%s: no migrations applied yet\n", label)
		return nil
	}
	if err != nil {
		return fmt.Errorf("read version: %w", err)
	}
	// A dirty schema means a previous run failed partway. Exiting non-zero stops
	// a rollout from proceeding against a half-migrated database.
	if dirty {
		return fmt.Errorf("schema is dirty at version %d: resolve manually, then run 'migrate force <version>'", version)
	}
	fmt.Printf("%s: version %d\n", label, version)
	return nil
}
