package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/documents"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestDocumentRootMustBeProvisionedBeforeStartup(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	base := t.TempDir()
	cfg.DocumentRoot = filepath.Join(base, "missing", "documents")
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "provision") {
		t.Errorf("missing provisioning not diagnosed: %v", err)
	}
	db, err := store.Open(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	svc, wake, stop, err := startDocuments(ctx, db, cfg)
	if stop != nil {
		stop(ctx)
	}
	if err == nil || svc != nil || wake != nil || stop != nil {
		t.Fatal("unprovisioned storage started service/workers", err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 0 {
		t.Fatal("startup created undurable directories", entries, err)
	}
}

func TestDocumentInitializationFailureCannotStartWork(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	db, err := store.Open(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB().Exec(`INSERT INTO customers(id,number,display_name) VALUES('buyer','1','Buyer')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DB().Exec(`INSERT INTO invoices(id,customer_id,state,currency,number,company_snapshot,customer_snapshot,payment_snapshot,locale_snapshot,tax_snapshot,note_snapshot,frozen_snapshot) VALUES('invoice','buyer','finalized','EUR','1','{}','{}','{}','{}','{}','{}','{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.DocumentRepository().Enqueue(ctx, "invoice"); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("storage initialization durability precondition failed")
	svc, wake, stop, err := startDocumentsWithStorage(ctx, db, cfg, func(string, int64) (*documents.Storage, error) {
		return nil, failure
	})
	if stop != nil {
		stop(ctx)
	}
	if !errors.Is(err, failure) || svc != nil || wake != nil || stop != nil {
		t.Fatal("initialization failure exposed document service", err)
	}
	var state, status string
	var attempts int
	if err = db.DB().QueryRow(`SELECT j.state,j.attempts,d.status FROM document_jobs j JOIN documents d ON d.id=j.document_id WHERE d.invoice_id='invoice'`).Scan(&state, &attempts, &status); err != nil {
		t.Fatal(err)
	}
	if state != "queued" || attempts != 0 || status != "pending" {
		t.Fatal("initialization failure consumed or published a job", state, attempts, status)
	}
	entries, err := os.ReadDir(cfg.DocumentRoot)
	if err != nil || len(entries) != 0 {
		t.Fatal("initialization failure exposed artifacts", entries, err)
	}
}

func TestDevelopmentDefaultRootRequiresExplicitProvisioning(t *testing.T) {
	ctx := context.Background()
	cfg := testConfig(t)
	cfg.Development, cfg.DocumentRoot = true, ""
	db, err := store.Open(ctx, cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, stop, err := startDocuments(ctx, db, cfg); !errors.Is(err, documents.ErrRootNotProvisioned) {
		if stop != nil {
			stop(ctx)
		}
		t.Fatal("development silently provisioned its default root", err)
	}
	root := filepath.Join(filepath.Dir(cfg.Database), "documents")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	_, _, stop, err := startDocuments(ctx, db, cfg)
	if err != nil {
		t.Fatal("preprovisioned development root rejected", err)
	}
	if err = stop(ctx); err != nil {
		t.Fatal(err)
	}
}
