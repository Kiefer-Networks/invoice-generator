//go:build !production

package devmode

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

const PaperlessToken = "local-paperless-fixture"

type fixtureDocument struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Data  []byte `json:"-"`
}
type Paperless struct {
	*httptest.Server
	mu       sync.Mutex
	state    string
	docs     []fixtureDocument
	rejected bool
}

// State is chosen by test configuration, never by a public application route.
func StartPaperless(state string) (*Paperless, error) {
	if !ValidPaperlessState(state) {
		return nil, errors.New("invalid fake Paperless state")
	}
	p := &Paperless{state: state}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serve))
	return p, nil
}
func ValidPaperlessState(s string) bool {
	return s == "accepted" || s == "delayed" || s == "rejected" || s == "timeout" || s == "reject-once"
}
func (p *Paperless) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Api-Version", "10")
	if r.Header.Get("Authorization") != "Token "+PaperlessToken {
		http.Error(w, "invalid fixture token", http.StatusUnauthorized)
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	switch r.URL.Path {
	case "/api/tags/":
		writeJSON(w, map[string]any{"count": 1, "results": []any{map[string]any{"id": 1, "name": r.URL.Query().Get("name__iexact")}}})
	case "/api/documents/post_document/":
		if r.Method != "POST" {
			http.Error(w, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if p.state == "rejected" || (p.state == "reject-once" && !p.rejected) {
			p.rejected = true
			http.Error(w, "fixture rejection", 400)
			return
		}
		if p.state == "timeout" {
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 21<<20)
		if r.ParseMultipartForm(1<<20) != nil { // #nosec G120 -- MaxBytesReader above enforces the 21 MiB total body limit.
			http.Error(w, "invalid upload", 400)
			return
		}
		defer func() { _ = r.MultipartForm.RemoveAll() }() // net/http also removes request multipart scratch files.
		f, _, e := r.FormFile("document")
		if e != nil {
			http.Error(w, "missing document", 400)
			return
		}
		defer func() { _ = f.Close() }() // Read-only input; reads and validation report their own errors.
		data, e := io.ReadAll(io.LimitReader(f, 20<<20))
		if e != nil {
			http.Error(w, "invalid document", 400)
			return
		}
		id := int64(len(p.docs) + 1)
		p.docs = append(p.docs, fixtureDocument{id, r.FormValue("title"), data})
		writeJSON(w, fmt.Sprintf("dev-task-%d", id))
	case "/api/tasks/":
		var id int64
		_, _ = fmt.Sscanf(r.URL.Query().Get("task_id"), "dev-task-%d", &id)
		results := []any{}
		if id > 0 && id <= int64(len(p.docs)) {
			status := "success"
			if p.state == "delayed" {
				status = "pending"
			}
			results = append(results, map[string]any{"task_id": fmt.Sprintf("dev-task-%d", id), "task_type": "consume_file", "trigger_source": "api_upload", "status": status, "related_document_ids": []int64{id}, "result_data": map[string]int64{"document_id": id}})
		}
		writeJSON(w, map[string]any{"count": len(results), "next": nil, "previous": nil, "results": results})
	case "/api/documents/":
		results := []fixtureDocument{}
		for _, d := range p.docs {
			if d.Title == r.URL.Query().Get("title__iexact") {
				results = append(results, d)
			}
		}
		writeJSON(w, map[string]any{"count": len(results), "results": results})
	default:
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) >= 3 && parts[0] == "api" && parts[1] == "documents" {
			id, e := strconv.Atoi(parts[2])
			if e == nil && id > 0 && id <= len(p.docs) {
				d := p.docs[id-1]
				if len(parts) == 4 && parts[3] == "download" {
					w.Header().Set("Content-Type", "application/pdf")
					_, _ = w.Write(d.Data)
				} else {
					writeJSON(w, d)
				}
				return
			}
		}
		http.NotFound(w, r)
	}
}
