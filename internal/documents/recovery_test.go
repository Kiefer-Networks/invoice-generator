package documents

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/render"
)

func TestGenerateFailureRetryDoesNotExposePartialDocument(t *testing.T) {
	s, db, d := serviceFixture(t)
	ctx := context.Background()
	f := invoicing.NewFinalizationService(db)
	key, e := f.Prepare(ctx, d.ID, d.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Finalize(ctx, d.ID, key); e != nil {
		t.Fatal(e)
	}
	j, e := db.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	s.render = func(context.Context, *render.TplData, string) error { return errors.New("render failed") }
	if e = s.Generate(ctx, j); e == nil {
		t.Fatal("failed render succeeded")
	}
	doc, e := db.DocumentRepository().Get(ctx, j.DocumentID)
	if e != nil || doc.Status == "ready" {
		t.Fatal(doc, e)
	}
	dir, e := s.storage.root.Open(".")
	if e != nil {
		t.Fatal(e)
	}
	defer dir.Close()
	entries, e := dir.ReadDir(-1)
	if e != nil || len(entries) != 0 {
		t.Fatal(entries, e)
	}
	if e = db.DocumentRepository().Fail(ctx, j, time.Now(), "render_failed", 1); e != nil {
		t.Fatal(e)
	}
	if e = db.DocumentRepository().Retry(ctx, j.DocumentID); e != nil {
		t.Fatal(e)
	}
	next, e := db.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil || next.Attempts != 1 {
		t.Fatal(next, e)
	}
}
func TestStorageSweepAbandonedFiles(t *testing.T) {
	s, e := NewStorage(t.TempDir(), 100)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	keep, e := s.Put(strings.NewReader("keep"))
	if e != nil {
		t.Fatal(e)
	}
	orphan, e := s.Put(strings.NewReader("orphan"))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Sweep(time.Now().Add(time.Hour), map[string]bool{keep.Key: true}); e != nil {
		t.Fatal(e)
	}
	if _, e = s.root.Stat(orphan.Key); e == nil {
		t.Fatal("orphan retained")
	}
	if _, e = s.root.Stat(keep.Key); e != nil {
		t.Fatal("ready artifact removed", e)
	}
}
func TestGenerateRecoveryRemovesOnlyAbandonedArtifacts(t *testing.T) {
	s, _, _ := serviceFixture(t)
	orphan, e := s.storage.Put(strings.NewReader("orphan"))
	if e != nil {
		t.Fatal(e)
	}
	past := time.Now().Add(-48 * time.Hour)
	if e = s.storage.root.Chtimes(orphan.Key, past, past); e != nil {
		t.Fatal(e)
	}
	fresh, e := s.storage.Put(strings.NewReader("fresh"))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = s.storage.root.Stat(orphan.Key); e == nil {
		t.Fatal("old orphan retained")
	}
	if _, e = s.storage.root.Stat(fresh.Key); e != nil {
		t.Fatal("fresh live write removed", e)
	}
}
func TestGenerateRejectsOversizedRendererBeforeAttachment(t *testing.T) {
	s, _, _ := serviceFixture(t)
	s.storage.max = 10
	s.render = func(_ context.Context, _ *render.TplData, path string) error {
		return os.WriteFile(path, []byte(strings.Repeat("x", 11)), 0600)
	}
	if _, e := s.renderPDF(context.Background(), &render.TplData{}, []byte("xml")); !errors.Is(e, ErrIntegrity) {
		t.Fatalf("oversize reached attachment parser: %v", e)
	}
}
func TestGenerateCommitFailureRetainsArtifactForSafeRecovery(t *testing.T) {
	s, db, d := serviceFixture(t)
	ctx := context.Background()
	f := invoicing.NewFinalizationService(db)
	key, e := f.Prepare(ctx, d.ID, d.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Finalize(ctx, d.ID, key); e != nil {
		t.Fatal(e)
	}
	j, e := db.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	_, e = db.DB().Exec(`CREATE TRIGGER deny_document_complete BEFORE UPDATE ON document_jobs WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'fail'); END`)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Generate(ctx, j); e == nil {
		t.Fatal("completion succeeded")
	}
	doc, e := db.DocumentRepository().Get(ctx, j.DocumentID)
	if e != nil || doc.Status == "ready" {
		t.Fatal(doc, e)
	}
	dir, e := s.storage.root.Open(".")
	if e != nil {
		t.Fatal(e)
	}
	defer dir.Close()
	entries, e := dir.ReadDir(-1)
	if e != nil || len(entries) != 1 {
		t.Fatal("artifact deleted before uncertain commit can be reconciled", entries, e)
	}
}
