package jobs

import (
	"context"
	"errors"
	"fmt"
	"github.com/kiefer-networks/invoice-generator/internal/documents"
	"github.com/kiefer-networks/invoice-generator/internal/paperless"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func paperlessFixture(t *testing.T) (*store.Store, *documents.Storage, store.Document) {
	t.Helper()
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	_, e = s.DB().Exec(`INSERT INTO customers(id,number,display_name) VALUES('pl','pl','Buyer'); INSERT INTO invoices(id,customer_id,state,currency,number,company_snapshot,customer_snapshot,payment_snapshot,locale_snapshot,tax_snapshot,note_snapshot,frozen_snapshot) VALUES('pl','pl','finalized','EUR','PL-1','{}','{}','{}','{}','{}','{}','{}')`)
	if e != nil {
		t.Fatal(e)
	}
	st, e := documents.NewStorage(t.TempDir(), paperless.MaxDocumentSize)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { st.Close() })
	a, e := st.Put(strings.NewReader("%PDF-test"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = s.DocumentRepository().Enqueue(ctx, "pl")
	if e != nil {
		t.Fatal(e)
	}
	j, e := s.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.DocumentRepository().Complete(ctx, j, store.Document{StorageKey: a.Key, SHA256: a.SHA256, Size: a.Size, GeneratorVersion: "test"}); e != nil {
		t.Fatal(e)
	}
	d, e := s.DocumentRepository().Get(ctx, j.DocumentID)
	if e != nil {
		t.Fatal(e)
	}
	return s, st, d
}
func TestPaperlessRestartReconcilesWithoutSecondUpload(t *testing.T) {
	for _, loseResponse := range []bool{false, true} {
		t.Run(fmt.Sprint(loseResponse), func(t *testing.T) {
			s, st, d := paperlessFixture(t)
			uploads := 0
			visible := false
			title := paperless.Title("PL-1", d.ID)
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/documents/":
					if visible {
						fmt.Fprintf(w, `{"count":1,"results":[{"id":42,"title":%q}]}`, title)
					} else {
						fmt.Fprint(w, `{"count":0,"results":[]}`)
					}
				case "/api/tags/":
					fmt.Fprintf(w, `{"count":1,"results":[{"id":1,"name":%q}]}`, r.URL.Query().Get("name__iexact"))
				case "/api/documents/post_document/":
					uploads++
					if loseResponse {
						fmt.Fprint(w, `malformed response`)
					} else {
						fmt.Fprint(w, `"task-123"`)
					}
				case "/api/tasks/":
					fmt.Fprint(w, `[{"task_id":"task-123","status":"STARTED"}]`)
				default:
					w.WriteHeader(404)
				}
			}))
			defer remote.Close()
			client, _ := paperless.NewClient(paperless.Config{URL: remote.URL, APIKey: "secret"}, remote.Client(), true)
			factory := func() (*paperless.Client, error) { return client, nil }
			w := NewPaperlessWorker(s, st, factory)
			ctx := context.Background()
			now := time.Now().Add(time.Second)
			j, e := s.PaperlessRepository().Claim(ctx, now, time.Minute)
			if e != nil {
				t.Fatal(e)
			}
			if e = w.Process(ctx, j); e == nil {
				t.Fatal("should be pending or uncertain")
			}
			saved, e := s.PaperlessRepository().ForDocument(ctx, d.ID)
			if e != nil || !saved.UploadStarted || (!loseResponse && saved.RemoteTaskID != "task-123") {
				t.Fatal(saved, e)
			}
			// Simulate process death with the lease still held, then restart after expiry.
			var seq int
			var name, databasePath string
			if e = s.DB().QueryRow("PRAGMA database_list").Scan(&seq, &name, &databasePath); e != nil {
				t.Fatal(e)
			}
			if e = s.Close(); e != nil {
				t.Fatal(e)
			}
			s, e = store.Open(ctx, databasePath)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { s.Close() })
			if e = s.Migrate(ctx); e != nil {
				t.Fatal(e)
			}
			w = NewPaperlessWorker(s, st, factory)
			j, e = s.PaperlessRepository().Claim(ctx, now.Add(2*time.Minute), time.Minute)
			if e != nil {
				t.Fatal(e)
			}
			if e = w.Process(ctx, j); e == nil {
				t.Fatal("not visible yet")
			}
			if uploads != 1 {
				t.Fatal("duplicate", uploads)
			}
			visible = true
			j, e = s.PaperlessRepository().Claim(ctx, now.Add(4*time.Minute), time.Minute)
			if e != nil {
				t.Fatal(e)
			}
			if e = w.Process(ctx, j); e != nil {
				t.Fatal(e)
			}
			saved, e = s.PaperlessRepository().ForDocument(ctx, d.ID)
			if e != nil || saved.State != "completed" || saved.RemoteDocumentID != 42 || uploads != 1 {
				t.Fatal(saved, e, uploads)
			}
		})
	}
}
func TestPaperlessWorkerMissingConfigurationRetryAndStop(t *testing.T) {
	s, st, d := paperlessFixture(t)
	w := NewPaperlessWorker(s, st, func() (*paperless.Client, error) { return nil, errors.New("configuration_missing") })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		j, e := s.PaperlessRepository().ForDocument(context.Background(), d.ID)
		if e == nil && j.ErrorCode == "configuration_missing" {
			if j.UploadStarted || j.State != "queued" {
				t.Fatal(j)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("worker did not stop")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatal("missing configuration not persisted")
}

func TestPaperlessExplicitRejectionCanRetryUpload(t *testing.T) {
	s, st, d := paperlessFixture(t)
	uploads := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/documents/":
			fmt.Fprint(w, `{"count":0,"results":[]}`)
		case "/api/tags/":
			fmt.Fprintf(w, `{"count":1,"results":[{"id":1,"name":%q}]}`, r.URL.Query().Get("name__iexact"))
		case "/api/documents/post_document/":
			uploads++
			if uploads == 1 {
				w.WriteHeader(429)
				fmt.Fprint(w, "secret")
			} else {
				fmt.Fprint(w, `"task-123"`)
			}
		case "/api/tasks/":
			fmt.Fprint(w, `[{"task_id":"task-123","status":"SUCCESS","related_document":"42"}]`)
		}
	}))
	defer remote.Close()
	c, _ := paperless.NewClient(paperless.Config{URL: remote.URL, APIKey: "secret"}, remote.Client(), true)
	w := NewPaperlessWorker(s, st, func() (*paperless.Client, error) { return c, nil })
	ctx := context.Background()
	now := time.Now().Add(time.Second)
	j, e := s.PaperlessRepository().Claim(ctx, now, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	e = w.Process(ctx, j)
	if e == nil || e.Error() != "request_failed" {
		t.Fatal(e)
	}
	state, _ := s.PaperlessRepository().ForDocument(ctx, d.ID)
	if state.UploadStarted {
		t.Fatal("definitive rejection left ambiguous marker")
	}
	if e = s.PaperlessRepository().Fail(ctx, j, now, "request_failed", 5); e != nil {
		t.Fatal(e)
	}
	j, e = s.PaperlessRepository().Claim(ctx, now.Add(time.Hour), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = w.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	state, _ = s.PaperlessRepository().ForDocument(ctx, d.ID)
	if state.State != "completed" || uploads != 2 {
		t.Fatal(state, uploads)
	}
}
