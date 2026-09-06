package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func paperlessReady(t *testing.T) (*Store, Document) {
	t.Helper()
	s, _, d := populatedDraft(t)
	ctx := context.Background()
	_, e := s.db.Exec(`UPDATE invoices SET state='finalized',number='PL-1',company_snapshot='{}',payment_snapshot='{}',locale_snapshot='{}',tax_snapshot='{}',note_snapshot='{}',frozen_snapshot='{}' WHERE id=?`, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PaperlessRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute); !errors.Is(e, ErrNotFound) {
		t.Fatal("pending document was queued", e)
	}
	j, e := s.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	e = s.DocumentRepository().Complete(ctx, j, Document{StorageKey: strings.Repeat("A", 52), SHA256: strings.Repeat("a", 64), Size: 4, GeneratorVersion: "test"})
	if e != nil {
		t.Fatal(e)
	}
	doc, e := s.DocumentRepository().Get(ctx, j.DocumentID)
	if e != nil {
		t.Fatal(e)
	}
	return s, doc
}
func TestPaperlessAtomicQueueFencingAndRetry(t *testing.T) {
	s, d := paperlessReady(t)
	ctx := context.Background()
	r := s.PaperlessRepository()
	now := time.Now().Add(time.Second)
	status, e := r.ForDocument(ctx, d.ID)
	if e != nil || status.State != "queued" {
		t.Fatal(status, e)
	}
	if e = r.Retry(ctx, d.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("retried queued", e)
	}
	j, e := r.Claim(ctx, now, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Claim(ctx, now, time.Minute); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	if e = r.BeginUpload(ctx, j); e != nil {
		t.Fatal(e)
	}
	newer, e := r.Claim(ctx, now.Add(2*time.Minute), time.Minute)
	if e != nil || !newer.UploadStarted || newer.Token == j.Token {
		t.Fatal(newer, e)
	}
	if e = r.SaveTask(ctx, j, "task-1"); !errors.Is(e, ErrConflict) {
		t.Fatal("stale lease", e)
	}
	if e = r.SaveTask(ctx, newer, "task-1"); e != nil {
		t.Fatal(e)
	}
	if e = r.Fail(ctx, newer, now, "untrusted secret detail", 2); e != nil {
		t.Fatal(e)
	}
	status, e = r.ForDocument(ctx, d.ID)
	if e != nil || status.State != "failed" || strings.Contains(status.ErrorSummary, "secret") {
		t.Fatal(status, e)
	}
	if e = r.Retry(ctx, d.ID); e != nil {
		t.Fatal(e)
	}
	retry, e := r.Claim(ctx, now.Add(time.Hour), time.Minute)
	if e != nil || retry.Attempts != 1 || retry.RemoteTaskID != "task-1" || !retry.UploadStarted {
		t.Fatal(retry, e)
	}
	if e = r.Complete(ctx, retry, 42); e != nil {
		t.Fatal(e)
	}
	status, e = r.ForDocument(ctx, d.ID)
	if e != nil || status.State != "completed" || status.RemoteDocumentID != 42 {
		t.Fatal(status, e)
	}
	if e = r.Retry(ctx, d.ID); !errors.Is(e, ErrConflict) {
		t.Fatal("retried success", e)
	}
}
func TestPaperlessRetryBackoffAndCrashBound(t *testing.T) {
	s, _ := paperlessReady(t)
	r := s.PaperlessRepository()
	ctx := context.Background()
	now := time.Now().Add(time.Second)
	j, e := r.Claim(ctx, now, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	for i := 1; i <= 5; i++ {
		if e = r.Fail(ctx, j, now, "configuration_missing", 5); e != nil {
			t.Fatal(e)
		}
		st, e := r.ForDocument(ctx, j.DocumentID)
		if e != nil {
			t.Fatal(e)
		}
		if i == 5 {
			if st.State != "failed" {
				t.Fatal(st)
			}
			break
		}
		delay := time.Unix(st.NextAttemptAt, 0).Sub(time.Unix(now.Unix(), 0))
		base := 30 * time.Second * time.Duration(1<<(i-1))
		if delay < base || delay > base+base/4 {
			t.Fatal("backoff", delay, base)
		}
		if _, e = r.Claim(ctx, time.Unix(st.NextAttemptAt-1, 0), time.Minute); !errors.Is(e, ErrNotFound) {
			t.Fatal("early", e)
		}
		now = time.Unix(st.NextAttemptAt, 0)
		j, e = r.Claim(ctx, now, time.Minute)
		if e != nil {
			t.Fatal(e)
		}
	}
	if e = r.Retry(ctx, j.DocumentID); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 5; i++ {
		j, e = r.Claim(ctx, now.Add(time.Hour+time.Duration(i)*2*time.Minute), time.Minute)
		if e != nil {
			t.Fatal(e)
		}
	}
	if _, e = r.Claim(ctx, now.Add(2*time.Hour), time.Minute); !errors.Is(e, ErrNotFound) {
		t.Fatal("unbounded crashes", e)
	}
}

func TestPaperlessExpiredLeaseCannotSubmitOrComplete(t *testing.T) {
	s, _ := paperlessReady(t)
	ctx := context.Background()
	r := s.PaperlessRepository()
	j, e := r.Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.db.Exec(`UPDATE paperless_jobs SET lease_expires_at=0 WHERE id=?`, j.ID); e != nil {
		t.Fatal(e)
	}
	if e = r.BeginUpload(ctx, j); !errors.Is(e, ErrConflict) {
		t.Fatal("expired upload accepted", e)
	}
	if e = r.Complete(ctx, j, 42); !errors.Is(e, ErrConflict) {
		t.Fatal("expired complete accepted", e)
	}
}

func TestPaperlessStateChangesHaveSafeAudit(t *testing.T) {
	s, d := paperlessReady(t)
	ctx := context.Background()
	r := s.PaperlessRepository()
	j, e := r.Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = r.Fail(ctx, j, time.Now(), "secret-token", 1); e != nil {
		t.Fatal(e)
	}
	if e = r.Retry(WithInvoiceAudit(ctx, "subject-admin", "request-1"), d.ID); e != nil {
		t.Fatal(e)
	}
	var n int
	if e = s.db.QueryRow(`SELECT count(*) FROM audit_events WHERE target_type='paperless_job' AND target_id=?`, j.ID).Scan(&n); e != nil || n < 3 {
		t.Fatal("missing delivery audit", n, e)
	}
	var actor string
	if e = s.db.QueryRow(`SELECT actor_subject FROM audit_events WHERE action='paperless.retry' AND target_id=?`, j.ID).Scan(&actor); e != nil || actor != "subject-admin" {
		t.Fatal(actor, e)
	}
	if e = s.db.QueryRow(`SELECT count(*) FROM audit_events WHERE change_summary LIKE '%secret-token%'`).Scan(&n); e != nil || n != 0 {
		t.Fatal(n, e)
	}
}

func TestPaperlessMigrationPreservesRemoteStateAndBackfillsReady(t *testing.T) {
	ctx := context.Background()
	s, e := Open(ctx, filepath.Join(t.TempDir(), "upgrade.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.ensureMigrationTable(ctx); e != nil {
		t.Fatal(e)
	}
	ms, e := embeddedMigrations()
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range ms {
		if m.version < 11 {
			if e = s.applyMigration(ctx, m); e != nil {
				t.Fatal(e)
			}
		}
	}
	_, e = s.db.Exec(`INSERT INTO customers(id,number,display_name) VALUES('pl','pl','Buyer'); INSERT INTO invoices(id,customer_id,state,currency,number,company_snapshot,customer_snapshot,payment_snapshot,locale_snapshot,tax_snapshot,note_snapshot,frozen_snapshot) VALUES('pl','pl','finalized','EUR','PL-1','{}','{}','{}','{}','{}','{}','{}')`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.DocumentRepository().Enqueue(ctx, "pl"); e != nil {
		t.Fatal(e)
	}
	j, e := s.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DocumentRepository().Complete(ctx, j, Document{StorageKey: strings.Repeat("A", 52), SHA256: strings.Repeat("a", 64), Size: 4, GeneratorVersion: "test"}); e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(`INSERT INTO paperless_jobs(id,document_id,state,attempts,next_attempt_at,lease_expires_at,remote_task_id) VALUES('historical',?,'leased',1,'2026-09-06T10:00:00Z','2026-09-06T10:05:00Z','task-123')`, j.DocumentID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	saved, e := s.PaperlessRepository().ForDocument(ctx, j.DocumentID)
	if e != nil || saved.State != "queued" || !saved.UploadStarted || saved.RemoteTaskID != "task-123" || saved.NextAttemptAt != 1788688800 {
		t.Fatal(saved, e)
	}
	for _, m := range ms {
		var checksum string
		if e = s.db.QueryRow(`SELECT checksum FROM schema_migrations WHERE version=?`, m.version).Scan(&checksum); e != nil || checksum != m.checksum {
			t.Fatal(m.version, e)
		}
	}
}

func TestPaperlessExpiredFinalLeaseBecomesManuallyRetryable(t *testing.T) {
	s, d := paperlessReady(t)
	ctx := context.Background()
	r := s.PaperlessRepository()
	j, e := r.Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.db.Exec(`UPDATE paperless_jobs SET attempts=5,lease_expires_at=0 WHERE id=?`, j.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = r.Claim(ctx, time.Now(), time.Minute); !errors.Is(e, ErrNotFound) {
		t.Fatal(e)
	}
	st, e := r.ForDocument(ctx, d.ID)
	if e != nil || st.State != "failed" {
		t.Fatal("expired terminal lease not failed", st, e)
	}
	if e = r.Retry(ctx, d.ID); e != nil {
		t.Fatal(e)
	}
}
