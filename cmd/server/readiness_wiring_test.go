//go:build !production

package main

import (
	"context"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"os"
	"path/filepath"
	"testing"
)

func TestReadinessActualServerWiring(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg, err := ParseConfig([]string{"-dev", "-dev-root", filepath.Join(t.TempDir(), "state")}, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	stop, err := prepareDevelopment(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	db, err := store.Open(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	manager, err := newAuthManager(ctx, db, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := readiness(ctx, db, cfg, manager)
	if err != nil {
		t.Fatal(err)
	}
	if err := ready(ctx); err != nil {
		t.Fatal(err)
	}
	var checksum string
	if err := db.DB().QueryRow("SELECT checksum FROM schema_migrations WHERE version=1").Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().Exec("UPDATE schema_migrations SET checksum='drift' WHERE version=1"); err != nil {
		t.Fatal(err)
	}
	if ready(ctx) == nil {
		t.Fatal("actual readiness accepted migration drift")
	}
	if _, err := db.DB().Exec("UPDATE schema_migrations SET checksum=? WHERE version=1", checksum); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(cfg.DocumentRoot); err != nil {
		t.Fatal(err)
	}
	if ready(ctx) == nil {
		t.Fatal("actual readiness accepted missing storage")
	}
	stop()
	if _, err := readiness(ctx, db, cfg, manager); err == nil {
		t.Fatal("runtime startup accepted unavailable OIDC discovery")
	}
}
