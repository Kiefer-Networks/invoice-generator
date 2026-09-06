package main

import (
	"context"
	"errors"
	"github.com/kiefer-networks/invoice-generator/internal/jobs"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"path/filepath"
	"testing"
	"time"
)

func TestCleanupPropagatesActiveWorkerTimeout(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DB().Exec(`INSERT INTO customers(id,number,display_name) VALUES('worker','worker','Buyer'); INSERT INTO invoices(id,customer_id,state,currency,number,company_snapshot,customer_snapshot,payment_snapshot,locale_snapshot,tax_snapshot,note_snapshot,frozen_snapshot) VALUES('worker','worker','finalized','EUR','WORKER','{}','{}','{}','{}','{}','{}','{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.DocumentRepository().Enqueue(ctx, "worker"); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	runner := jobs.New(db.DocumentRepository(), func(context.Context, store.DocumentJob) error { close(entered); <-release; return context.Canceled })
	if err := runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		close(release)
		end, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		_ = runner.Stop(end)
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("job did not start")
	}
	err = finishService(nil, func(ctx context.Context) error {
		bounded, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		return runner.Stop(bounded)
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("active worker timeout lost: %v", err)
	}
}
