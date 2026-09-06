package jobs

import (
	"context"
	"errors"
	"fmt"
	"github.com/kiefer-networks/invoice-generator/internal/documents"
	"github.com/kiefer-networks/invoice-generator/internal/paperless"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
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
						if e := r.ParseMultipartForm(1 << 20); e != nil {
							t.Error(e)
							return
						}
						conn, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Error(e)
							return
						}
						conn.Close() // accepted bytes, lost response after the write
					} else {
						fmt.Fprint(w, `"task-123"`)
					}
				case "/api/tasks/":
					if visible {
						fmt.Fprint(w, `[{"task_id":"task-123","status":"SUCCESS","related_document":42}]`)
					} else {
						fmt.Fprint(w, `[{"task_id":"task-123","status":"STARTED"}]`)
					}
				case "/api/documents/42/":
					fmt.Fprintf(w, `{"id":42,"title":%q}`, title)
				case "/api/documents/42/download/":
					fmt.Fprint(w, "%PDF-test")
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
		case "/api/documents/42/":
			fmt.Fprintf(w, `{"id":42,"title":%q}`, paperless.Title("PL-1", d.ID))
		case "/api/documents/42/download/":
			fmt.Fprint(w, "%PDF-test")
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

func TestPaperlessKnownTaskAndContentRequiredForAdoption(t *testing.T) {
	for _, mode := range []string{"wrong_bytes", "task_disagreement", "pending_task", "duplicate", "valid"} {
		t.Run(mode, func(t *testing.T) {
			s, st, d := paperlessFixture(t)
			title := paperless.Title("PL-1", d.ID)
			var calls []string
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				switch r.URL.Path {
				case "/api/tasks/":
					if mode == "pending_task" {
						fmt.Fprint(w, `[{"task_id":"task-123","status":"STARTED"}]`)
					} else {
						fmt.Fprint(w, `[{"task_id":"task-123","status":"SUCCESS","related_document":43}]`)
					}
				case "/api/documents/":
					if mode == "duplicate" {
						fmt.Fprintf(w, `{"count":2,"results":[{"id":42,"title":%q},{"id":43,"title":%q}]}`, title, title)
					} else {
						fmt.Fprintf(w, `{"count":1,"results":[{"id":42,"title":%q}]}`, title)
					}
				case "/api/documents/42/":
					fmt.Fprintf(w, `{"id":42,"title":%q}`, title)
				case "/api/documents/42/download/":
					if mode == "wrong_bytes" {
						fmt.Fprint(w, "%PDF-evil")
					} else {
						fmt.Fprint(w, "%PDF-test")
					}
				default:
					w.WriteHeader(404)
				}
			}))
			defer remote.Close()
			c, _ := paperless.NewClient(paperless.Config{URL: remote.URL, APIKey: "secret"}, remote.Client(), true)
			worker := NewPaperlessWorker(s, st, func() (*paperless.Client, error) { return c, nil })
			ctx := context.Background()
			j, e := s.PaperlessRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.PaperlessRepository().BeginUpload(ctx, j); e != nil {
				t.Fatal(e)
			}
			if mode == "task_disagreement" || mode == "pending_task" {
				if e = s.PaperlessRepository().SaveTask(ctx, j, "task-123"); e != nil {
					t.Fatal(e)
				}
			}
			e = worker.Process(ctx, j)
			saved, _ := s.PaperlessRepository().ForDocument(ctx, d.ID)
			if mode == "valid" {
				if e != nil || saved.State != "completed" {
					t.Fatal(saved, e)
				}
			} else {
				if e == nil || saved.State == "completed" {
					t.Fatal("unverified candidate delivered", mode, saved, e)
				}
			}
			if (mode == "task_disagreement" || mode == "pending_task") && (len(calls) == 0 || calls[0] != "/api/tasks/") {
				t.Fatal("known task not reconciled first", calls)
			}
		})
	}
}

func TestPaperlessPreSendFailureAfterTagsRetriesOnce(t *testing.T) {
	s, st, d := paperlessFixture(t)
	tags, uploads := 0, 0
	title := paperless.Title("PL-1", d.ID)
	var remote *httptest.Server
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/documents/":
			fmt.Fprint(w, `{"count":0,"results":[]}`)
		case "/api/tags/":
			tags++
			if tags == 6 {
				remote.Listener.Close()
			}
			fmt.Fprintf(w, `{"count":1,"results":[{"id":1,"name":%q}]}`, r.URL.Query().Get("name__iexact"))
		case "/api/documents/post_document/":
			uploads++
			fmt.Fprint(w, `"task-123"`)
		case "/api/tasks/":
			fmt.Fprint(w, `[{"task_id":"task-123","status":"SUCCESS","related_document":42}]`)
		case "/api/documents/42/":
			fmt.Fprintf(w, `{"id":42,"title":%q}`, title)
		case "/api/documents/42/download/":
			fmt.Fprint(w, "%PDF-test")
		default:
			w.WriteHeader(404)
		}
	})
	remote = httptest.NewServer(handler)
	defer remote.Close()
	address := remote.Listener.Addr().String()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableKeepAlives = true
	client, e := paperless.NewClient(paperless.Config{URL: remote.URL, APIKey: "secret"}, &http.Client{Transport: tr}, true)
	if e != nil {
		t.Fatal(e)
	}
	worker := NewPaperlessWorker(s, st, func() (*paperless.Client, error) { return client, nil })
	ctx := context.Background()
	now := time.Now().Add(time.Second)
	j, e := s.PaperlessRepository().Claim(ctx, now, time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.Process(ctx, j); e == nil || e.Error() != "request_failed" {
		t.Fatal(e)
	}
	saved, _ := s.PaperlessRepository().ForDocument(ctx, d.ID)
	if saved.UploadStarted || uploads != 0 || tags != 6 {
		t.Fatal("pre-send failure retained upload intent", saved, uploads, tags)
	}
	if e = s.PaperlessRepository().Fail(ctx, j, now, "request_failed", 5); e != nil {
		t.Fatal(e)
	}
	ln, e := net.Listen("tcp", address)
	if e != nil {
		t.Fatal(e)
	}
	recovered := httptest.NewUnstartedServer(handler)
	recovered.Listener.Close()
	recovered.Listener = ln
	recovered.Start()
	defer recovered.Close()
	j, e = s.PaperlessRepository().Claim(ctx, now.Add(time.Hour), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.Process(ctx, j); e != nil {
		t.Fatal(e)
	}
	saved, _ = s.PaperlessRepository().ForDocument(ctx, d.ID)
	if saved.State != "completed" || uploads != 1 {
		t.Fatal(saved, uploads)
	}
}

func TestPaperlessCancelledBeforeUploadPersistsSafeRetry(t *testing.T) {
	s, st, d := paperlessFixture(t)
	uploads := 0
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/documents/":
			fmt.Fprint(w, `{"count":0,"results":[]}`)
		case "/api/tags/":
			fmt.Fprintf(w, `{"count":1,"results":[{"id":1,"name":%q}]}`, r.URL.Query().Get("name__iexact"))
		default:
			uploads++
			w.WriteHeader(500)
		}
	}))
	defer remote.Close()
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DisableKeepAlives = true
	c, _ := paperless.NewClient(paperless.Config{URL: remote.URL, APIKey: "secret"}, &http.Client{Transport: tr}, true)
	worker := NewPaperlessWorker(s, st, func() (*paperless.Client, error) { return c, nil })
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	ctx := httptrace.WithClientTrace(parent, &httptrace.ClientTrace{GetConn: func(string) {
		calls++
		if calls == 8 {
			cancel()
		}
	}})
	j, e := s.PaperlessRepository().Claim(context.Background(), time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = worker.Process(ctx, j); e == nil {
		t.Fatal("cancelled operation completed")
	}
	saved, _ := s.PaperlessRepository().ForDocument(context.Background(), d.ID)
	if saved.UploadStarted || uploads != 0 || calls != 8 {
		t.Fatal("cancelled proven-unsent request retained intent", saved, uploads, calls)
	}
}
