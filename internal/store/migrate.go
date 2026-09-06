package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version  int
	name     string
	checksum string
	sql      string
}

// Migrate applies every embedded migration exactly once. A previously applied
// migration must retain its original contents, otherwise Migrate rejects it.
func (s *Store) Migrate(ctx context.Context) error {
	if err := s.ensureMigrationTable(ctx); err != nil {
		return err
	}

	migrations, err := embeddedMigrations()
	if err != nil {
		return err
	}
	for _, migration := range migrations {
		if err := s.applyMigration(ctx, migration); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ensureMigrationTable(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			checksum TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)`)
	if err != nil {
		return fmt.Errorf("create schema migrations table: %w", err)
	}
	return nil
}

func embeddedMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(strings.TrimSuffix(entry.Name(), ".sql"), "_", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid migration name %q", entry.Name())
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil || version < 1 {
			return nil, fmt.Errorf("invalid migration version in %q", entry.Name())
		}
		contents, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		hash := sha256.Sum256(contents)
		migrations = append(migrations, migration{
			version:  version,
			name:     parts[1],
			checksum: hex.EncodeToString(hash[:]),
			sql:      string(contents),
		})
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	for i := 1; i < len(migrations); i++ {
		if migrations[i-1].version == migrations[i].version {
			return nil, fmt.Errorf("duplicate migration version %d", migrations[i].version)
		}
	}
	return migrations, nil
}

func (s *Store) applyMigration(ctx context.Context, migration migration) (err error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("get connection for migration %03d: %w", migration.version, err)
	}
	defer func() { _ = conn.Close() }()

	var name, checksum string
	err = conn.QueryRowContext(ctx, "SELECT name, checksum FROM schema_migrations WHERE version = ?", migration.version).Scan(&name, &checksum)
	switch {
	case err == nil:
		if name != migration.name || checksum != migration.checksum {
			return fmt.Errorf("migration %03d checksum drift", migration.version)
		}
		return nil
	case err != nil && !isNoRows(err):
		return fmt.Errorf("read migration %03d state: %w", migration.version, err)
	}

	if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin migration %03d: %w", migration.version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	var existingName, existingChecksum string
	err = conn.QueryRowContext(ctx, "SELECT name, checksum FROM schema_migrations WHERE version = ?", migration.version).Scan(&existingName, &existingChecksum)
	switch {
	case err == nil:
		if existingName != migration.name || existingChecksum != migration.checksum {
			return fmt.Errorf("migration %03d checksum drift", migration.version)
		}
	case err != nil && !isNoRows(err):
		return fmt.Errorf("read locked migration %03d state: %w", migration.version, err)
	case isNoRows(err):
		if _, err = conn.ExecContext(ctx, migration.sql); err != nil {
			return fmt.Errorf("apply migration %03d: %w", migration.version, err)
		}
		if migration.version == 3 || migration.version == 4 {
			if err = backfillCustomerKeysOnConn(ctx, conn); err != nil {
				return fmt.Errorf("backfill migration %03d: %w", migration.version, err)
			}
		}
		if migration.version == 5 {
			if err = backfillCatalogKeysOnConn(ctx, conn); err != nil {
				return fmt.Errorf("backfill migration %03d: %w", migration.version, err)
			}
		}
		if _, err = conn.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, ?)",
			migration.version, migration.name, migration.checksum, time.Now().UTC().Format(time.RFC3339Nano),
		); err != nil {
			return fmt.Errorf("record migration %03d: %w", migration.version, err)
		}
	}
	if _, err = conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migration %03d: %w", migration.version, err)
	}
	committed = true
	return nil
}

func isNoRows(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
