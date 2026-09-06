package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestReadinessRequiresCompleteMigrations(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "ready.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.CheckMigrations(ctx) == nil {
		t.Fatal("unmigrated database ready")
	}
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.CheckMigrations(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().ExecContext(ctx, "UPDATE schema_migrations SET checksum='changed' WHERE version=1"); err != nil {
		t.Fatal(err)
	}
	if db.CheckMigrations(ctx) == nil {
		t.Fatal("drifted migrations ready")
	}
}
