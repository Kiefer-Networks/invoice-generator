//go:build !production

package devmode

import (
	"context"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestSeedIdempotentAndRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "development.sqlite")
	if _, e := PrepareRoot(filepath.Dir(path)); e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if e = db.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = Seed(ctx, db); e != nil {
		t.Fatal(e)
	}
	document, e := db.DocumentRepository().ForInvoice(ctx, "dev-invoice-finalized-0001")
	if e != nil || document.ID != "dev-document-finalized-0001" {
		t.Fatalf("fixture document identifier is not deterministic: %s %v", document.ID, e)
	}
	for table, want := range map[string]int{"companies": 1, "customers": 2, "catalog_items": 2, "invoices": 3, "document_jobs": 2, "paperless_jobs": 1} {
		var n int
		if e = db.DB().QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); e != nil || n != want {
			t.Fatalf("%s count %d: %v", table, n, e)
		}
	}
	if _, e = db.DB().ExecContext(ctx, "UPDATE customers SET display_name='User edit' WHERE id='dev-customer-1'"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	db, e = store.Open(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = Seed(ctx, db); e != nil {
		t.Fatal(e)
	}
	c, e := db.CustomerRepository().Get(ctx, "dev-customer-1")
	if e != nil || c.DisplayName != "User edit" {
		t.Fatalf("restart overwrote edits: %+v %v", c, e)
	}
	f, e := db.InvoiceRepository().GetFinalized(ctx, "dev-invoice-finalized-0001")
	if e != nil || f.Number != "DEV-2026-0001" {
		t.Fatalf("fixed invoice: %+v %v", f, e)
	}
}

func TestSeedRecoversInterruptedInitialization(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if _, e := PrepareRoot(root); e != nil {
		t.Fatal(e)
	}
	db, e := store.Open(ctx, filepath.Join(root, "development.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if e = db.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = seedBusiness(ctx, db); e != nil {
		t.Fatal(e)
	}
	if _, e = db.DocumentRepository().Claim(ctx, time.Now(), 5*time.Minute); e != nil {
		t.Fatal(e)
	}
	if e = Seed(ctx, db); e != nil {
		t.Fatalf("restart after interrupted generation: %v", e)
	}
	var count int
	if e = db.DB().QueryRow("SELECT count(*) FROM paperless_jobs WHERE state='failed'").Scan(&count); e != nil || count != 1 {
		t.Fatalf("failed fixture missing %d %v", count, e)
	}
}
