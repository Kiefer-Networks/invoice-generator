package paperless

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("failed to write temp file: %v", err)
	}
	return path
}

func TestLoadYAML(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "paperless.yaml", `
url: "https://paperless.example.com"
api_key: "abc123"
tags:
  - "Invoices"
  - "2026"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.URL != "https://paperless.example.com" || cfg.APIKey != "abc123" {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if len(cfg.Tags) != 2 || cfg.Tags[0] != "Invoices" {
		t.Errorf("unexpected tags: %v", cfg.Tags)
	}
}

func TestLoadTOML(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "paperless.toml", `
url = "https://paperless.example.com"
api_key = "abc123"
tags = ["Invoices"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(toml) failed: %v", err)
	}
	if cfg.URL != "https://paperless.example.com" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

func TestLoadMissingRequiredFields(t *testing.T) {
	dir := t.TempDir()
	path := writeTemp(t, dir, "paperless.yaml", `api_key: "abc123"`)
	if _, err := Load(path); err == nil {
		t.Error("expected error for missing url, got nil")
	}

	path2 := writeTemp(t, dir, "paperless2.yaml", `url: "https://paperless.example.com"`)
	if _, err := Load(path2); err == nil {
		t.Error("expected error for missing api_key, got nil")
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "does-not-exist.yaml")); err == nil {
		t.Error("expected error loading missing file, got nil")
	}
}

func TestLoadTooLarge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "huge.yaml")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("could not create file: %v", err)
	}
	if err := f.Truncate(maxConfigFileSize + 1); err != nil {
		f.Close()
		t.Fatalf("could not truncate file: %v", err)
	}
	f.Close()

	if _, err := Load(path); err == nil {
		t.Error("expected error for oversized config file, got nil")
	} else if !strings.Contains(err.Error(), "too large") {
		t.Errorf("expected 'too large' error, got: %v", err)
	}
}

func TestLoadWithLocalOverrideAppliesWhenPresent(t *testing.T) {
	dir := t.TempDir()
	base := writeTemp(t, dir, "paperless.yaml", `
url: "https://template.example.com"
api_key: "template-key"
tags:
  - "Invoices"
`)
	writeTemp(t, dir, "paperless.local.yaml", `
url: "https://real.example.com"
api_key: "real-key"
`)

	cfg, overridePath, err := LoadWithLocalOverride(base)
	if err != nil {
		t.Fatalf("LoadWithLocalOverride failed: %v", err)
	}
	if overridePath == "" {
		t.Fatal("expected an override path")
	}
	if cfg.URL != "https://real.example.com" || cfg.APIKey != "real-key" {
		t.Errorf("expected override values, got %+v", cfg)
	}
	if len(cfg.Tags) != 1 || cfg.Tags[0] != "Invoices" {
		t.Errorf("expected tags to fall through from base, got %v", cfg.Tags)
	}
}

func TestLoadWithLocalOverrideAbsentIsNoop(t *testing.T) {
	dir := t.TempDir()
	base := writeTemp(t, dir, "paperless.yaml", `
url: "https://template.example.com"
api_key: "template-key"
`)
	cfg, overridePath, err := LoadWithLocalOverride(base)
	if err != nil {
		t.Fatalf("LoadWithLocalOverride failed: %v", err)
	}
	if overridePath != "" {
		t.Errorf("expected no override path, got %q", overridePath)
	}
	if cfg.URL != "https://template.example.com" {
		t.Errorf("unexpected config: %+v", cfg)
	}
}

// TestUploadCreatesTagAndPostsDocument spins up a fake Paperless-ngx
// server and verifies Upload resolves/creates the configured tags and
// posts the document with the right auth header and multipart fields.
func TestUploadCreatesTagAndPostsDocument(t *testing.T) {
	var sawTagLookup, sawTagCreate, sawUpload bool
	var uploadAuth, uploadTitle string
	var uploadTagField string

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token test-key" {
			t.Errorf("missing/incorrect auth header on tag request: %q", r.Header.Get("Authorization"))
		}
		switch r.Method {
		case http.MethodGet:
			sawTagLookup = true
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(tagListResponse{Count: 0})
		case http.MethodPost:
			sawTagCreate = true
			w.WriteHeader(http.StatusCreated)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(tagCreateResponse{ID: 42})
		}
	})
	mux.HandleFunc("/api/documents/post_document/", func(w http.ResponseWriter, r *http.Request) {
		sawUpload = true
		uploadAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			t.Fatalf("failed to parse multipart form: %v", err)
		}
		uploadTitle = r.FormValue("title")
		uploadTagField = r.FormValue("tags")
		if _, _, err := r.FormFile("document"); err != nil {
			t.Errorf("expected a document file part: %v", err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`"task-uuid-123"`))
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	dir := t.TempDir()
	docPath := filepath.Join(dir, "invoice.pdf")
	if err := os.WriteFile(docPath, []byte("%PDF-fake-content"), 0600); err != nil {
		t.Fatalf("failed to write fake pdf: %v", err)
	}

	cfg := &Config{URL: server.URL, APIKey: "test-key", Tags: []string{"Invoices"}}
	if err := Upload(cfg, docPath, "My Invoice"); err != nil {
		t.Fatalf("Upload failed: %v", err)
	}

	if !sawTagLookup || !sawTagCreate || !sawUpload {
		t.Errorf("expected tag lookup, tag creation, and upload, got lookup=%v create=%v upload=%v", sawTagLookup, sawTagCreate, sawUpload)
	}
	if uploadAuth != "Token test-key" {
		t.Errorf("expected auth header 'Token test-key', got %q", uploadAuth)
	}
	if uploadTitle != "My Invoice" {
		t.Errorf("expected title 'My Invoice', got %q", uploadTitle)
	}
	if uploadTagField != "42" {
		t.Errorf("expected resolved tag id '42', got %q", uploadTagField)
	}
}

func TestUploadReusesExistingTag(t *testing.T) {
	var sawTagCreate bool

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tags/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			sawTagCreate = true
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tagListResponse{
			Count: 1,
			Results: []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			}{{ID: 7, Name: "Invoices"}},
		})
	})
	mux.HandleFunc("/api/documents/post_document/", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(10 << 20)
		if got := r.FormValue("tags"); got != "7" {
			t.Errorf("expected existing tag id '7', got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	dir := t.TempDir()
	docPath := filepath.Join(dir, "invoice.pdf")
	os.WriteFile(docPath, []byte("%PDF-fake-content"), 0600)

	cfg := &Config{URL: server.URL, APIKey: "test-key", Tags: []string{"Invoices"}}
	if err := Upload(cfg, docPath, "title"); err != nil {
		t.Fatalf("Upload failed: %v", err)
	}
	if sawTagCreate {
		t.Error("expected existing tag to be reused, not recreated")
	}
}

func TestUploadFailsOnServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/documents/post_document/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	dir := t.TempDir()
	docPath := filepath.Join(dir, "invoice.pdf")
	os.WriteFile(docPath, []byte("%PDF-fake-content"), 0600)

	cfg := &Config{URL: server.URL, APIKey: "test-key"}
	if err := Upload(cfg, docPath, "title"); err == nil {
		t.Error("expected error on server 500, got nil")
	}
}
