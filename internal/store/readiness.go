package store

import (
	"context"
	"errors"
)

// CheckMigrations checks every embedded migration without applying changes.
func (s *Store) CheckMigrations(ctx context.Context) error {
	expected, err := embeddedMigrations()
	if err != nil {
		return err
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count); err != nil {
		return err
	}
	if count != len(expected) {
		return errors.New("migration state incomplete")
	}
	for _, m := range expected {
		var checksum string
		if err := s.db.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE version=? AND name=?", m.version, m.name).Scan(&checksum); err != nil {
			return err
		}
		if checksum != m.checksum {
			return errors.New("migration checksum mismatch")
		}
	}
	return nil
}
