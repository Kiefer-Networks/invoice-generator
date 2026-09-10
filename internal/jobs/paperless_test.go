package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/documents"
	"github.com/kiefer-networks/invoice-generator/internal/paperless"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func paperlessFixture(t *testing.T) (*store.Store, *documents.Storage, store.Document) {
	t.Helper()
	ctx := context.Background()
	s, e := store.Open(ctx, filepath.Join(t.TempDir(), "jobs.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = s.Close() })
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
	t.Cleanup(func() { _ = st.Close() })
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
				if r.Header.Get("Accept") != "application/json; version=10" {
					t.Error("unversioned API request", r.URL.Path)
				}
				w.Header().Set("X-Api-Version", "10")
				switch r.URL.Path {
				case "/api/documents/":
					if visible {
						_, _ = fmt.Fprintf(w, `{"count":1,"results":[{"id":42,"title":%q}]}`, title)
					} else {
						_, _ = fmt.Fprint(w, `{"count":0,"results":[]}`)
					}
				case "/api/tags/":
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Content-Type", "application/json")
					w.Header().Set("Content-Type", "application/json")
					_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "results": []map[string]any{{"id": 1, "name": r.URL.Query().Get("name__iexact")}}})
				case "/api/documents/post_document/":
					uploads++
					if loseResponse {
						r.Body = http.MaxBytesReader(w, r.Body, 21<<20)
						if e := r.ParseMultipartForm(1 << 20); e != nil { // #nosec G120 -- MaxBytesReader above bounds the total mock upload body.
							t.Error(e)
							return
						}
						conn, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Error(e)
							return
						}
						_ = conn.Close() // accepted bytes, lost response after the write
					} else {
						_, _ = fmt.Fprint(w, `"task-123"`)
					}
				case "/api/tasks/":
					if visible {
						paperlessTaskResponse(w, "success", 42)
					} else {
						paperlessTaskResponse(w, "started", 0)
					}
				case "/api/documents/42/":
					_, _ = fmt.Fprintf(w, `{"id":42,"title":%q}`, title)
				case "/api/documents/42/download/":
					if r.URL.Query().Get("original") != "true" {
						t.Error("archive requested instead of original")
					}
					_, _ = fmt.Fprint(w, "%PDF-test")
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
			t.Cleanup(func() { _ = s.Close() })
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
			_, _ = fmt.Fprint(w, `{"count":0,"results":[]}`)
		case "/api/tags/":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "results": []map[string]any{{"id": 1, "name": r.URL.Query().Get("name__iexact")}}})
		case "/api/documents/post_document/":
			uploads++
			if uploads == 1 {
				w.WriteHeader(429)
				_, _ = fmt.Fprint(w, "secret")
			} else {
				_, _ = fmt.Fprint(w, `"task-123"`)
			}
		case "/api/tasks/":
			paperlessTaskResponse(w, "success", 42)
		case "/api/documents/42/":
			_, _ = fmt.Fprintf(w, `{"id":42,"title":%q}`, paperless.Title("PL-1", d.ID))
		case "/api/documents/42/download/":
			_, _ = fmt.Fprint(w, "%PDF-test")
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
						paperlessTaskResponse(w, "started", 0)
					} else {
						paperlessTaskResponse(w, "success", 43)
					}
				case "/api/documents/":
					if mode == "duplicate" {
						_, _ = fmt.Fprintf(w, `{"count":2,"results":[{"id":42,"title":%q},{"id":43,"title":%q}]}`, title, title)
					} else {
						_, _ = fmt.Fprintf(w, `{"count":1,"results":[{"id":42,"title":%q}]}`, title)
					}
				case "/api/documents/42/":
					_, _ = fmt.Fprintf(w, `{"id":42,"title":%q}`, title)
				case "/api/documents/42/download/":
					if mode == "wrong_bytes" {
						_, _ = fmt.Fprint(w, "%PDF-evil")
					} else {
						_, _ = fmt.Fprint(w, "%PDF-test")
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
			_, _ = fmt.Fprint(w, `{"count":0,"results":[]}`)
		case "/api/tags/":
			tags++
			if tags == 6 {
				_ = remote.Listener.Close()
			}
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "results": []map[string]any{{"id": 1, "name": r.URL.Query().Get("name__iexact")}}})
		case "/api/documents/post_document/":
			uploads++
			_, _ = fmt.Fprint(w, `"task-123"`)
		case "/api/tasks/":
			paperlessTaskResponse(w, "success", 42)
		case "/api/documents/42/":
			_, _ = fmt.Fprintf(w, `{"id":42,"title":%q}`, title)
		case "/api/documents/42/download/":
			_, _ = fmt.Fprint(w, "%PDF-test")
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
	_ = recovered.Listener.Close()
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
			_, _ = fmt.Fprint(w, `{"count":0,"results":[]}`)
		case "/api/tags/":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"count": 1, "results": []map[string]any{{"id": 1, "name": r.URL.Query().Get("name__iexact")}}})
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

func TestPaperlessIncompatibleAPIIsVisible(t *testing.T) {
	s, st, d := paperlessFixture(t)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(406)
		_, _ = fmt.Fprint(w, `{"detail":"secret-token"}`)
	}))
	defer remote.Close()
	c, _ := paperless.NewClient(paperless.Config{URL: remote.URL, APIKey: "secret-token"}, remote.Client(), true)
	worker := NewPaperlessWorker(s, st, func() (*paperless.Client, error) { return c, nil })
	ctx := context.Background()
	j, e := s.PaperlessRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	e = worker.Process(ctx, j)
	if !errors.Is(e, paperless.ErrAPIIncompatible) {
		t.Fatal("incompatibility hidden", e)
	}
	if e = s.PaperlessRepository().Fail(ctx, j, time.Now(), e.Error(), 1); e != nil {
		t.Fatal(e)
	}
	saved, e := s.PaperlessRepository().ForDocument(ctx, d.ID)
	if e != nil || saved.ErrorCode != "api_incompatible" || !strings.Contains(saved.ErrorSummary, "API 10") || saved.UploadStarted {
		t.Fatal(saved, e)
	}
}

// Paperless-ngx v3.1.3 TaskSerializerV10's paginated consumption task response.
func paperlessTaskResponse(w http.ResponseWriter, status string, id int64) {
	result, ids := "null", "[]"
	if id > 0 {
		result = fmt.Sprintf(`{"document_id":%d}`, id)
		ids = fmt.Sprintf(`[%d]`, id)
	}
	_, _ = fmt.Fprintf(w, `{"count":1,"next":null,"previous":null,"results":[{"id":7,"task_id":"task-123","task_type":"consume_file","trigger_source":"api_upload","status":%q,"result_data":%s,"related_document_ids":%s,"acknowledged":false}]}`, status, result, ids)
}
