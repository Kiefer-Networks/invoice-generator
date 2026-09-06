package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"
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

func TestSchemaRejectsInvoiceItemReassignmentToFinalizedInvoice(t *testing.T) {
	t.Parallel()

	s := openMigratedStore(t)
	ctx := context.Background()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO customers (id, number, display_name) VALUES ('customer-reassign', 'C-REASSIGN', 'Customer')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO invoices (id, customer_id, state, currency) VALUES ('invoice-draft', 'customer-reassign', 'draft', 'EUR')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO invoices (id, customer_id, state, number, currency, company_snapshot, customer_snapshot, payment_snapshot, locale_snapshot, tax_snapshot, note_snapshot) VALUES ('invoice-finalized', 'customer-reassign', 'finalized', 'F-REASSIGN', 'EUR', '{}', '{}', '{}', '{}', '{}', '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO invoice_items (id, invoice_id, position, title_snapshot, quantity_scaled, net_unit_price_minor, tax_rate_scaled) VALUES ('item-reassign', 'invoice-draft', 1, 'Draft item', 1000, 100, 1900)`); err != nil {
		t.Fatal(err)
	}

	mustReject(t, s.db, ctx, `UPDATE invoice_items SET invoice_id = 'invoice-finalized' WHERE id = 'item-reassign'`)
}

func TestSchemaRejectsFractionalScaledAndMoneyValues(t *testing.T) {
	t.Parallel()

	s := openMigratedStore(t)
	ctx := context.Background()
	for _, column := range []string{"net_unit_price_minor", "tax_rate_scaled"} {
		mustReject(t, s.db, ctx, fmt.Sprintf(`INSERT INTO catalog_items (id, number, title, kind, net_unit_price_minor, tax_rate_scaled) VALUES ('catalog-%[1]s', 'C-%[1]s', 'Catalog item', 'service', %[2]s, %[3]s)`, column, fractionalOrInteger(column, "net_unit_price_minor", "100"), fractionalOrInteger(column, "tax_rate_scaled", "1900")))
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO customers (id, number, display_name) VALUES ('customer-fractional', 'C-FRACTIONAL', 'Customer')`); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"net_total_minor", "tax_total_minor", "gross_total_minor"} {
		mustReject(t, s.db, ctx, fmt.Sprintf(`INSERT INTO invoices (id, customer_id, state, number, currency, company_snapshot, customer_snapshot, payment_snapshot, locale_snapshot, tax_snapshot, note_snapshot, %[1]s) VALUES ('invoice-%[1]s', 'customer-fractional', 'finalized', 'F-%[1]s', 'EUR', '{}', '{}', '{}', '{}', '{}', '{}', 1.5)`, column))
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO invoices (id, customer_id, state, currency) VALUES ('invoice-item-fractional', 'customer-fractional', 'draft', 'EUR')`); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"quantity_scaled", "net_unit_price_minor", "tax_rate_scaled", "net_total_minor", "tax_total_minor", "gross_total_minor"} {
		mustReject(t, s.db, ctx, fmt.Sprintf(`INSERT INTO invoice_items (id, invoice_id, position, title_snapshot, quantity_scaled, net_unit_price_minor, tax_rate_scaled, net_total_minor, tax_total_minor, gross_total_minor) VALUES ('item-%[1]s', 'invoice-item-fractional', 1, 'Item', %[2]s, %[3]s, %[4]s, %[5]s, %[6]s, %[7]s)`, column, fractionalOrInteger(column, "quantity_scaled", "1000"), fractionalOrInteger(column, "net_unit_price_minor", "100"), fractionalOrInteger(column, "tax_rate_scaled", "1900"), fractionalOrInteger(column, "net_total_minor", "100000"), fractionalOrInteger(column, "tax_total_minor", "19000"), fractionalOrInteger(column, "gross_total_minor", "119000")))
	}
}

func TestMigrateRejectsNameDrift(t *testing.T) {
	t.Parallel()

	s := openMigratedStore(t)
	if _, err := s.db.ExecContext(context.Background(), `UPDATE schema_migrations SET name = 'changed' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err == nil {
		t.Fatal("Migrate accepted a changed migration name")
	}
}

func TestMigrateUpgradesOriginalSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.ensureMigrationTable(ctx); err != nil {
		t.Fatal(err)
	}
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	initial := migrations[0]
	if _, err := s.db.ExecContext(ctx, initial.sql); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, '2026-09-06T00:00:00Z')`, initial.version, initial.name, initial.checksum); err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("upgrade original schema: %v", err)
	}
	var migrationsApplied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&migrationsApplied); err != nil {
		t.Fatal(err)
	}
	if migrationsApplied != 8 {
		t.Fatalf("migration count=%d, want 8", migrationsApplied)
	}
}

func TestCustomerKeyMigrationBackfillsPopulatedDatabaseUnderWriterLock(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.ensureMigrationTable(ctx); err != nil {
		t.Fatal(err)
	}
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range migrations[:2] {
		if _, err := s.db.ExecContext(ctx, item.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, '2026-09-06T00:00:00Z')`, item.version, item.name, item.checksum); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO customers (id, number, display_name, country, preferred_language, currency) VALUES ('legacy-customer', 'C-001', 'Éclair Studio', 'DE', 'de', 'EUR')`); err != nil {
		t.Fatal(err)
	}

	locker, err := s.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	if _, err := locker.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	err = s.Migrate(blocked)
	cancel()
	if err == nil {
		t.Fatal("migration proceeded while another SQLite writer held BEGIN IMMEDIATE")
	}
	if _, err := locker.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var searchKey, sortKey string
	if err := s.db.QueryRowContext(ctx, `SELECT search_key, sort_key FROM customers WHERE id='legacy-customer'`).Scan(&searchKey, &sortKey); err != nil {
		t.Fatal(err)
	}
	wantSearch, wantSort := customerKeys(CustomerInput{Number: "C-001", DisplayName: "Éclair Studio"})
	if searchKey != wantSearch || sortKey != wantSort {
		t.Fatalf("backfilled keys = %q, %q; want %q, %q", searchKey, sortKey, wantSearch, wantSort)
	}
}

func TestCustomerKeyRepairMigrationUpgradesRecorded003WithoutChecksumDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.ensureMigrationTable(ctx); err != nil {
		t.Fatal(err)
	}
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 4 {
		t.Fatalf("repair migration is missing: %#v", migrations)
	}
	for _, item := range migrations[:3] {
		if _, err := s.db.ExecContext(ctx, item.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, '2026-09-06T00:00:00Z')`, item.version, item.name, item.checksum); err != nil {
			t.Fatal(err)
		}
	}
	customers := []struct{ id, number, name, searchKey, sortKey string }{
		{"legacy-eclair", "C-001", "Éclair Studio", "stale", "stale"},
		{"legacy-ecole", "C-002", "École Conseil", "", ""},
	}
	for _, customer := range customers {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO customers (id, number, display_name, country, preferred_language, currency, search_key, sort_key) VALUES (?, ?, ?, 'DE', 'de', 'EUR', ?, ?)`, customer.id, customer.number, customer.name, customer.searchKey, customer.sortKey); err != nil {
			t.Fatal(err)
		}
	}
	var beforeChecksum string
	if err := s.db.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version=3`).Scan(&beforeChecksum); err != nil {
		t.Fatal(err)
	}

	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	for _, customer := range customers {
		var searchKey, sortKey string
		if err := s.db.QueryRowContext(ctx, `SELECT search_key, sort_key FROM customers WHERE id=?`, customer.id).Scan(&searchKey, &sortKey); err != nil {
			t.Fatal(err)
		}
		wantSearch, wantSort := customerKeys(CustomerInput{Number: customer.number, DisplayName: customer.name})
		if searchKey != wantSearch || sortKey != wantSort {
			t.Fatalf("%s keys = %q, %q; want %q, %q", customer.id, searchKey, sortKey, wantSearch, wantSort)
		}
	}
	page, err := s.CustomerRepository().List(ctx, CustomerListOptions{Search: "é", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Customers) != 1 || page.Customers[0].ID != "legacy-eclair" || page.NextCursor == "" {
		t.Fatalf("repaired first page = %#v", page)
	}
	page, err = s.CustomerRepository().List(ctx, CustomerListOptions{Search: "é", Limit: 1, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Customers) != 1 || page.Customers[0].ID != "legacy-ecole" || page.NextCursor != "" {
		t.Fatalf("repaired second page = %#v", page)
	}
	var afterChecksum string
	if err := s.db.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version=3`).Scan(&afterChecksum); err != nil {
		t.Fatal(err)
	}
	if afterChecksum != beforeChecksum {
		t.Fatalf("migration 003 checksum changed from %q to %q", beforeChecksum, afterChecksum)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var applied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 8 {
		t.Fatalf("migration count after idempotent repair = %d, want 8", applied)
	}
}

func TestCatalogKeyMigrationUpgradesPre005DatabaseWithoutChecksumDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.ensureMigrationTable(ctx); err != nil {
		t.Fatal(err)
	}
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) < 5 {
		t.Fatalf("catalog migration is missing: %#v", migrations)
	}
	for _, migration := range migrations[:4] {
		if _, err := s.db.ExecContext(ctx, migration.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, '2026-09-06T00:00:00Z')`, migration.version, migration.name, migration.checksum); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct{ id, number, title string }{{"legacy-eclair", "G-001", "Éclair service"}, {"legacy-ecole", "G-002", "École service"}} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO catalog_items (id, number, kind, title, unit, net_unit_price_minor, tax_rate_scaled) VALUES (?, ?, 'service', ?, 'hour', 1250, 1900)`, item.id, item.number, item.title); err != nil {
			t.Fatal(err)
		}
	}
	var beforeChecksum string
	if err := s.db.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version=4`).Scan(&beforeChecksum); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := s.CatalogRepository().List(ctx, CatalogListOptions{Search: "é", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "legacy-eclair" || page.NextCursor == "" {
		t.Fatalf("first upgraded catalog page=%#v, %v", page, err)
	}
	page, err = s.CatalogRepository().List(ctx, CatalogListOptions{Search: "é", Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "legacy-ecole" || page.NextCursor != "" {
		t.Fatalf("second upgraded catalog page=%#v, %v", page, err)
	}
	var afterChecksum string
	if err := s.db.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version=4`).Scan(&afterChecksum); err != nil {
		t.Fatal(err)
	}
	if afterChecksum != beforeChecksum {
		t.Fatalf("migration 004 checksum changed from %q to %q", beforeChecksum, afterChecksum)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var applied int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_migrations`).Scan(&applied); err != nil || applied != 8 {
		t.Fatalf("idempotent catalog migration count=%d, %v", applied, err)
	}
}

func TestInvoiceDraftDiscountMigrationUpgradesPre006DatabaseWithoutChecksumDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.ensureMigrationTable(ctx); err != nil {
		t.Fatal(err)
	}
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, migration := range migrations[:5] {
		if _, err := s.db.ExecContext(ctx, migration.sql); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, checksum, applied_at) VALUES (?, ?, ?, '2026-09-06T00:00:00Z')`, migration.version, migration.name, migration.checksum); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO customers (id, number, display_name, country, preferred_language, currency) VALUES ('customer-1', 'C-001', 'Acme', 'DE', 'de', 'EUR')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO invoices (id, customer_id, state, currency) VALUES ('invoice-1', 'customer-1', 'draft', 'EUR')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO invoice_items (id, invoice_id, position, title_snapshot, unit_snapshot, quantity_scaled, net_unit_price_minor, tax_rate_scaled) VALUES ('line-1', 'invoice-1', 1, 'Advice', 'hour', 10000, 100, 1900)`); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.db.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version=5`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var discount int
	if err := s.db.QueryRowContext(ctx, `SELECT discount_basis_points FROM invoice_items WHERE id='line-1'`).Scan(&discount); err != nil || discount != 0 {
		t.Fatalf("discount=%d err=%v", discount, err)
	}
	var after string
	if err := s.db.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version=5`).Scan(&after); err != nil || after != before {
		t.Fatalf("migration 005 checksum=%q err=%v", after, err)
	}
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

func fractionalOrInteger(column, fractionalColumn, integer string) string {
	if column == fractionalColumn {
		return "1.5"
	}
	return integer
}

func TestFinalizationMigrationUpgrades006AndPreservesChecksums(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.ensureMigrationTable(ctx); err != nil {
		t.Fatal(err)
	}
	ms, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms[:6] {
		if err = s.applyMigration(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.DB().Exec(`INSERT INTO customers(id,number,display_name,country,currency) VALUES('c','C','Buyer','DE','EUR'); INSERT INTO invoices(id,customer_id,state,currency) VALUES('draft','c','draft','EUR')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = s.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range ms[:6] {
		var checksum string
		if err = s.DB().QueryRow(`SELECT checksum FROM schema_migrations WHERE version=?`, m.version).Scan(&checksum); err != nil || checksum != m.checksum {
			t.Fatalf("checksum %d=%q %v", m.version, checksum, err)
		}
	}
	var state string
	var frozen, key, paid sql.NullString
	var seq sql.NullInt64
	if err = s.DB().QueryRow(`SELECT state,frozen_snapshot,finalization_key,paid_at,invoice_sequence FROM invoices WHERE id='draft'`).Scan(&state, &frozen, &key, &paid, &seq); err != nil || state != "draft" || frozen.Valid || key.Valid || paid.Valid || seq.Valid {
		t.Fatalf("upgrade state=%s %v", state, err)
	}
	if _, err = s.DB().Exec(`UPDATE invoices SET invoice_sequence=1.5 WHERE id='draft'`); err == nil {
		t.Fatal("fractional sequence accepted")
	}
}
