package documents

import (
	"context"
	"errors"
	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"os"
	"strings"
	"testing"
	"time"
)

func TestStoragePublicationBarrierFailureRetainsUnpublishedFile(t *testing.T) {
	s, e := NewStorage(t.TempDir(), 100)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	failure := errors.New("metadata flush failed")
	var sawFinal string
	s.publicationBarrier = func(root *os.Root, key string) error {
		dir, e := root.Open(".")
		if e != nil {
			t.Fatal(e)
		}
		entries, e := dir.ReadDir(-1)
		dir.Close()
		if e != nil || len(entries) != 1 || entries[0].Name() != key {
			t.Fatalf("barrier did not run after atomic rename: %v %v", entries, e)
		}
		sawFinal = key
		return failure
	}
	a, e := s.Put(strings.NewReader("durable"))
	if !errors.Is(e, failure) || a.Key != "" || sawFinal == "" {
		t.Fatal(a, e, sawFinal)
	}
	if _, e = s.root.Stat(sawFinal); e != nil {
		t.Fatal("ambiguous publication was destructively removed", e)
	}
	if e = s.Sweep(time.Now().Add(time.Hour), map[string]bool{}); e != nil {
		t.Fatal(e)
	}
}
func TestGeneratePublicationBarrierFailureIsRetryable(t *testing.T) {
	s, db, d := serviceFixture(t)
	ctx := context.Background()
	fs := invoicing.NewFinalizationService(db)
	key, e := fs.Prepare(ctx, d.ID, d.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = fs.Finalize(ctx, d.ID, key); e != nil {
		t.Fatal(e)
	}
	j, e := db.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	s.storage.publicationBarrier = func(*os.Root, string) error { return errors.New("flush failed") }
	if e = s.Generate(ctx, j); e == nil {
		t.Fatal("generation published without durability barrier")
	}
	doc, e := db.DocumentRepository().Get(ctx, j.DocumentID)
	if e != nil || doc.Status == "ready" {
		t.Fatal(doc, e)
	}
	var state string
	if e = db.DB().QueryRow(`SELECT state FROM document_jobs WHERE id=?`, j.ID).Scan(&state); e != nil || state != "leased" {
		t.Fatal(state, e)
	}
	if e = db.DocumentRepository().Fail(ctx, j, time.Now(), "render_failed", 1); e != nil {
		t.Fatal(e)
	}
	if e = db.DocumentRepository().Retry(ctx, j.DocumentID); e != nil {
		t.Fatal(e)
	}
	s.storage.publicationBarrier = syncPublication
	next, e := db.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Generate(ctx, next); e != nil {
		t.Fatal(e)
	}
	doc, e = db.DocumentRepository().Get(ctx, j.DocumentID)
	if e != nil || doc.Status != "ready" {
		t.Fatal(doc, e)
	}
}
func TestStorageNativePublicationBarrierPropagatesErrors(t *testing.T) {
	s, e := NewStorage(t.TempDir(), 100)
	if e != nil {
		t.Fatal(e)
	}
	s.Close()
	if e = syncPublication(s.root, strings.Repeat("A", 52)); e == nil {
		t.Fatal("native barrier hid an invalid handle")
	}
}
