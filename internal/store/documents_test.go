package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDocumentJobsLeaseRetryAndImmutability(t *testing.T) {
	s, _, d := populatedDraft(t)
	ctx := context.Background()
	r := s.DocumentRepository()
	if _, e := r.Enqueue(ctx, d.ID); e == nil {
		t.Fatal("draft queued")
	}
	_, e := s.db.Exec(`UPDATE invoices SET state='finalized',number='DOC-1',company_snapshot='{}',payment_snapshot='{}',locale_snapshot='{}',tax_snapshot='{}',note_snapshot='{}',frozen_snapshot='{}' WHERE id=?`, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	doc, e := r.Enqueue(ctx, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	again, e := r.Enqueue(ctx, d.ID)
	if e != nil || again.ID != doc.ID {
		t.Fatal(again, e)
	}
	now := time.Now().UTC().Add(time.Second)
	j, e := r.Claim(ctx, now, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Claim(ctx, now, time.Minute); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	recovered, e := r.Claim(ctx, now.Add(2*time.Minute), time.Minute)
	if e != nil || recovered.Attempts != 2 || recovered.Token == j.Token {
		t.Fatal(recovered, e)
	}
	if e = r.Complete(ctx, j, Document{StorageKey: strings.Repeat("A", 52), SHA256: strings.Repeat("a", 64), Size: 3, GeneratorVersion: "1"}); !errors.Is(e, ErrConflict) {
		t.Fatal("stale lease", e)
	}
	if e = r.Fail(ctx, recovered, now.Add(2*time.Minute), "render_failed", 3); e != nil {
		t.Fatal(e)
	}
	if _, e = r.Claim(ctx, now.Add(2*time.Minute), time.Minute); !errors.Is(e, ErrNotFound) {
		t.Fatal("retry too soon", e)
	}
	last, e := r.Claim(ctx, now.Add(5*time.Minute), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	artifact := Document{StorageKey: strings.Repeat("B", 52), SHA256: strings.Repeat("b", 64), Size: 3, GeneratorVersion: "test"}
	if e = r.Complete(ctx, last, artifact); e != nil {
		t.Fatal(e)
	}
	got, e := r.Get(ctx, doc.ID)
	if e != nil || got.Status != "ready" || got.SHA256 != artifact.SHA256 {
		t.Fatal(got, e)
	}
	if _, e = r.Claim(ctx, now.Add(time.Hour), time.Minute); !errors.Is(e, ErrNotFound) {
		t.Fatal("completed reclaimed", e)
	}
	for _, q := range []string{`UPDATE documents SET size_bytes=5 WHERE id=?`, `DELETE FROM documents WHERE id=?`, `UPDATE OR REPLACE documents SET rowid=rowid+100 WHERE id=?`} {
		if _, e = s.db.Exec(q, doc.ID); e == nil {
			t.Fatal("immutable artifact changed", q)
		}
	}
	if _, e = r.Authorized(ctx, "", doc.ID); !errors.Is(e, ErrNotFound) {
		t.Fatal("anonymous authorized", e)
	}
}
func TestDocumentMigrationUpgrade(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, filepath.Join(t.TempDir(), "upgrade.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.ensureMigrationTable(ctx); e != nil {
		t.Fatal(e)
	}
	ms, _ := embeddedMigrations()
	for _, m := range ms {
		if m.version < 10 {
			if e := s.applyMigration(ctx, m); e != nil {
				t.Fatal(e)
			}
		}
	}
	var before int
	s.db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&before)
	if before != 9 {
		t.Fatalf("expected actual pre010 DB, got %d", before)
	}
	if e := s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e := s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	for _, m := range ms {
		var sum string
		if e := s.db.QueryRow(`SELECT checksum FROM schema_migrations WHERE version=?`, m.version).Scan(&sum); e != nil || sum != m.checksum {
			t.Fatal(m.version, sum, e)
		}
	}
}
func TestDocumentRetryLimitAndLeaseExpiry(t *testing.T) {
	s, _, d := populatedDraft(t)
	ctx := context.Background()
	_, e := s.db.Exec(`UPDATE invoices SET state='finalized',number='DOC-LIMIT',company_snapshot='{}',payment_snapshot='{}',locale_snapshot='{}',tax_snapshot='{}',note_snapshot='{}',frozen_snapshot='{}' WHERE id=?`, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	r := s.DocumentRepository()
	now := time.Now().Add(time.Second)
	var last DocumentJob
	for i := 0; i < 5; i++ {
		last, e = r.Claim(ctx, now.Add(time.Duration(i)*2*time.Minute), time.Minute)
		if e != nil {
			t.Fatal(i, e)
		}
	}
	if _, e = r.Claim(ctx, now.Add(time.Hour), time.Minute); !errors.Is(e, ErrNotFound) {
		t.Fatal("crashed jobs exceed maximum attempts", e)
	}
	doc, e := r.Get(ctx, last.DocumentID)
	if e != nil || doc.Status != "failed" {
		t.Fatal(doc, e)
	}
	if e = r.Retry(ctx, doc.ID); e != nil {
		t.Fatal(e)
	}
	j, e := r.Claim(ctx, now.Add(time.Hour), time.Minute)
	if e != nil || j.Attempts != 1 {
		t.Fatal(j, e)
	}
}
func TestDocumentRecoveryQueuesExistingFrozenInvoice(t *testing.T) {
	s, _, d := populatedDraft(t)
	ctx := context.Background()
	_, e := s.db.Exec(`UPDATE invoices SET state='finalized',number='RECOVERY',company_snapshot='{}',payment_snapshot='{}',locale_snapshot='{}',tax_snapshot='{}',note_snapshot='{}',frozen_snapshot='{}' WHERE id=?`, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	doc, e := s.DocumentRepository().ForInvoice(ctx, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(`DELETE FROM document_jobs WHERE document_id=?`, doc.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DocumentRepository().Enqueue(ctx, d.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute); e != nil {
		t.Fatal("missing pending job not recovered", e)
	}
}
func TestDocumentCompletionBindsLeaseToDocument(t *testing.T) {
	s, _, d := populatedDraft(t)
	ctx := context.Background()
	_, e := s.db.Exec(`UPDATE invoices SET state='finalized',number='BIND',company_snapshot='{}',payment_snapshot='{}',locale_snapshot='{}',tax_snapshot='{}',note_snapshot='{}',frozen_snapshot='{}' WHERE id=?`, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	r := s.DocumentRepository()
	j, e := r.Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	original := j.DocumentID
	j.DocumentID = "wrong"
	if e = r.Fail(ctx, j, time.Now(), "render_failed", 5); !errors.Is(e, ErrConflict) {
		t.Fatal("lease accepted different document", e)
	}
	j.DocumentID = original
	if e = r.Fail(ctx, j, time.Now(), "render_failed", 5); e != nil {
		t.Fatal("rejected command changed actual job", e)
	}
}
