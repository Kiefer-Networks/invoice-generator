package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func TestMigrateCreatesSchemaAndIsIdempotent(t *testing.T) {
	t.Parallel()

	s := openMigratedStore(t)

	wantTables := []string{
		"schema_migrations", "companies", "customers", "catalog_items", "invoices",
		"invoice_items", "documents", "paperless_jobs", "oidc_users", "sessions", "audit_events",
	}
	for _, table := range wantTables {
		var name string
		err := s.db.QueryRowContext(context.Background(), `
			SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?`, table,
		).Scan(&name)
		if err != nil {
			t.Fatalf("migration did not create %s: %v", table, err)
		}
	}

	var before int
	if err := s.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second migration: %v", err)
	}
	var after int
	if err := s.db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM schema_migrations").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("second migration changed migration count from %d to %d", before, after)
	}
}

func TestSchemaConstraints(t *testing.T) {
	t.Parallel()

	s := openMigratedStore(t)
	ctx := context.Background()

	mustReject(t, s.db, ctx, `INSERT INTO invoices (id, customer_id, state, currency) VALUES ('invoice-fk', 'missing', 'draft', 'EUR')`)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO companies (id, legal_name) VALUES ('company-1', 'Kiefer Networks')`); err != nil {
		t.Fatal(err)
	}
	mustReject(t, s.db, ctx, `INSERT INTO companies (id, legal_name) VALUES ('company-2', 'Second company')`)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO customers (id, number, display_name, active) VALUES ('customer-1', 'C-001', 'Customer', 1)`); err != nil {
		t.Fatal(err)
	}
	mustReject(t, s.db, ctx, `INSERT INTO customers (id, number, display_name, active) VALUES ('customer-2', 'C-001', 'Other customer', 1)`)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO catalog_items (id, number, title, kind, net_unit_price_minor, tax_rate_scaled, active) VALUES ('item-0', 'I-000', 'Valid item', 'service', 100, 1900, 1)`); err != nil {
		t.Fatal(err)
	}
	mustReject(t, s.db, ctx, `INSERT INTO catalog_items (id, number, title, kind, net_unit_price_minor, tax_rate_scaled, active) VALUES ('item-duplicate', 'I-000', 'Duplicate item', 'service', 100, 1900, 1)`)
	mustReject(t, s.db, ctx, `INSERT INTO catalog_items (id, number, title, kind, net_unit_price_minor, tax_rate_scaled, active) VALUES ('item-invalid-kind', 'I-002', 'Invalid item', 'other', 100, 1900, 1)`)
	mustReject(t, s.db, ctx, `INSERT INTO catalog_items (id, number, title, kind, net_unit_price_minor, tax_rate_scaled, active) VALUES ('item-1', 'I-001', 'Item', 'service', -1, 1900, 1)`)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO oidc_users (id, issuer, subject, display_name, active) VALUES ('user-1', 'https://issuer.example', 'subject', 'User', 1)`); err != nil {
		t.Fatal(err)
	}
	mustReject(t, s.db, ctx, `INSERT INTO oidc_users (id, issuer, subject, display_name, active) VALUES ('user-2', 'https://issuer.example', 'subject', 'Other user', 1)`)

	if _, err := s.db.ExecContext(ctx, `INSERT INTO customers (id, number, display_name, active) VALUES ('customer-3', 'C-002', 'Invoice customer', 1)`); err != nil {
		t.Fatal(err)
	}
	mustReject(t, s.db, ctx, `INSERT INTO invoices (id, customer_id, state, currency) VALUES ('invoice-state', 'customer-3', 'invalid', 'EUR')`)
	mustReject(t, s.db, ctx, `INSERT INTO invoices (id, customer_id, state, number, currency) VALUES ('invoice-final', 'customer-3', 'finalized', '2026-0001', 'EUR')`)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO invoices (id, customer_id, state, number, currency, company_snapshot, customer_snapshot, payment_snapshot, locale_snapshot, tax_snapshot, note_snapshot) VALUES ('invoice-number', 'customer-3', 'finalized', '2026-0001', 'EUR', '{}', '{}', '{}', '{}', '{}', '{}')`); err != nil {
		t.Fatal(err)
	}
	mustReject(t, s.db, ctx, `INSERT INTO invoices (id, customer_id, state, number, currency, company_snapshot, customer_snapshot, payment_snapshot, locale_snapshot, tax_snapshot, note_snapshot) VALUES ('invoice-duplicate-number', 'customer-3', 'finalized', '2026-0001', 'EUR', '{}', '{}', '{}', '{}', '{}', '{}')`)
	mustReject(t, s.db, ctx, `INSERT INTO invoice_items (id, invoice_id, position, title_snapshot, quantity_scaled, net_unit_price_minor, tax_rate_scaled) VALUES ('item-final', 'invoice-number', 1, 'Immutable item', 1000, 100, 1900)`)
}

func openMigratedStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func mustReject(t *testing.T, db *sql.DB, ctx context.Context, statement string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, statement); err == nil {
		t.Fatalf("statement unexpectedly succeeded: %s", statement)
	}
}
