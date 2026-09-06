package web

import (
	"context"
	"github.com/kiefer-networks/invoice-generator/internal/documents"
	"github.com/kiefer-networks/invoice-generator/internal/invoicing"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDocumentDownloadAuthorizationHeadersRangesAndIntegrity(t *testing.T) {
	authn := &fakeAuth{}
	_, db := customerApp(t, authn)
	d := finalWebDraft(t, db)
	ctx := context.Background()
	fs := invoicing.NewFinalizationService(db)
	key, e := fs.Prepare(ctx, d.ID, d.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = fs.Finalize(ctx, d.ID, key); e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	storage, e := documents.NewStorage(root, 1000)
	if e != nil {
		t.Fatal(e)
	}
	defer storage.Close()
	a, e := storage.Put(strings.NewReader("%PDF-test-document"))
	if e != nil {
		t.Fatal(e)
	}
	j, e := db.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.DocumentRepository().Complete(ctx, j, store.Document{StorageKey: a.Key, SHA256: a.SHA256, Size: a.Size, GeneratorVersion: "test"}); e != nil {
		t.Fatal(e)
	}
	svc := documents.New(db, storage)
	h, e := New(Dependencies{Auth: authn, Store: db, Documents: svc, Config: Config{AllowedHosts: []string{"app.example.test"}}})
	if e != nil {
		t.Fatal(e)
	}
	path := "/documents/" + j.DocumentID + "/download"
	request := func(path, rangeHeader string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://app.example.test"+path, nil)
		r.Header.Set("Range", rangeHeader)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request(path, ""); w.Code != 404 {
		t.Fatalf("unowned %d", w.Code)
	}
	if _, e = db.AuthRepository().UpsertUser(ctx, store.OIDCUser{ID: "u1", Issuer: "https://issuer", Subject: "subject-ada"}); e != nil {
		t.Fatal(e)
	}
	w := request(path, "")
	if w.Code != 200 || w.Body.String() != "%PDF-test-document" {
		t.Fatal(w.Code, w.Body.String())
	}
	for k, v := range map[string]string{"Content-Type": "application/pdf", "X-Content-Type-Options": "nosniff", "Cache-Control": "private, no-store", "Content-Disposition": "attachment; filename=invoice.pdf"} {
		if w.Header().Get(k) != v {
			t.Errorf("%s=%s", k, w.Header().Get(k))
		}
	}
	w = request(path, "bytes=0-3")
	if w.Code != 206 || w.Body.String() != "%PDF" {
		t.Fatal(w.Code, w.Body.String())
	}
	w = request(path, "bytes=999-1000")
	if w.Code != 416 {
		t.Fatal(w.Code)
	}
	for _, p := range []string{"/documents/../secret/download", "/documents/%2e%2e/download", "/documents/abc%5cdef/download", "/documents/missing/download"} {
		w = request(p, "")
		if w.Code != 404 {
			t.Fatalf("unsafe %s %d", p, w.Code)
		}
	}
	os.WriteFile(filepath.Join(root, a.Key), []byte("tampered"), 0600)
	w = request(path, "")
	if w.Code != 500 || strings.Contains(w.Body.String(), root) {
		t.Fatal(w.Code, w.Body.String())
	}
	authn.authenticateErr = context.Canceled
	w = request(path, "")
	if w.Code != 302 {
		t.Fatal(w.Code)
	}
}
func TestPreviewRouteRequiresAuthentication(t *testing.T) {
	h, _ := customerApp(t, &fakeAuth{authenticateErr: context.Canceled})
	w := customerRequest(t, h, http.MethodGet, "/invoices/any/preview", nil, false)
	if w.Code != 302 {
		t.Fatal(w.Code)
	}
}
func TestDocumentPreviewAndStatusPages(t *testing.T) {
	authn := &fakeAuth{}
	_, db := customerApp(t, authn)
	d := finalWebDraft(t, db)
	ctx := context.Background()
	if _, e := db.AuthRepository().UpsertUser(ctx, store.OIDCUser{ID: "u1", Issuer: "https://issuer", Subject: "subject-ada"}); e != nil {
		t.Fatal(e)
	}
	st, e := documents.NewStorage(t.TempDir(), 20<<20)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	h, e := New(Dependencies{Auth: authn, Store: db, Documents: documents.New(db, st), Config: Config{AllowedHosts: []string{"app.example.test"}}})
	if e != nil {
		t.Fatal(e)
	}
	w := customerRequest(t, h, "GET", "/invoices/"+d.ID+"/preview", nil, false)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/pdf" || !strings.HasPrefix(w.Body.String(), "%PDF-") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = customerRequest(t, h, "GET", "/invoices/"+d.ID+"/preview", nil, true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "1.19") {
		t.Fatal(w.Code, w.Body.String())
	}
	f := invoicing.NewFinalizationService(db)
	key, e := f.Prepare(ctx, d.ID, d.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.Finalize(ctx, d.ID, key); e != nil {
		t.Fatal(e)
	}
	for _, hx := range []bool{false, true} {
		w = customerRequest(t, h, "GET", "/invoices/"+d.ID, nil, hx)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Document: pending") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	j, e := db.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.DocumentRepository().Fail(ctx, j, time.Now(), "render_failed", 1); e != nil {
		t.Fatal(e)
	}
	path := "/documents/" + j.DocumentID + "/retry"
	authn.csrfErr = context.Canceled
	w = customerRequest(t, h, "POST", path, url.Values{}, false)
	if w.Code != 403 {
		t.Fatal("retry CSRF", w.Code)
	}
	authn.csrfErr = nil
	authn.csrfErr = nil
	w = customerRequest(t, h, "POST", path, url.Values{}, true)
	if w.Code != 200 || w.Header().Get("HX-Redirect") != "/invoices/"+d.ID {
		t.Fatal(w.Code, w.Header())
	}
}

func TestPaperlessStatusAndProtectedManualRetry(t *testing.T) {
	authn := &fakeAuth{}
	_, db := customerApp(t, authn)
	d := finalWebDraft(t, db)
	ctx := context.Background()
	fs := invoicing.NewFinalizationService(db)
	key, e := fs.Prepare(ctx, d.ID, d.Version)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = fs.Finalize(ctx, d.ID, key); e != nil {
		t.Fatal(e)
	}
	st, e := documents.NewStorage(t.TempDir(), 1000)
	if e != nil {
		t.Fatal(e)
	}
	defer st.Close()
	a, e := st.Put(strings.NewReader("%PDF"))
	if e != nil {
		t.Fatal(e)
	}
	j, e := db.DocumentRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.DocumentRepository().Complete(ctx, j, store.Document{StorageKey: a.Key, SHA256: a.SHA256, Size: a.Size, GeneratorVersion: "test"}); e != nil {
		t.Fatal(e)
	}
	h, e := New(Dependencies{Auth: authn, Store: db, Documents: documents.New(db, st), Config: Config{AllowedHosts: []string{"app.example.test"}}})
	if e != nil {
		t.Fatal(e)
	}
	w := customerRequest(t, h, "GET", "/invoices/"+d.ID, nil, false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Paperless: queued") {
		t.Fatal(w.Code, w.Body.String())
	}
	path := "/documents/" + j.DocumentID + "/paperless-retry"
	w = customerRequest(t, h, "POST", path, url.Values{}, false)
	if w.Code != 404 {
		t.Fatal("inactive user", w.Code)
	}
	if _, e = db.AuthRepository().UpsertUser(ctx, store.OIDCUser{ID: "u1", Issuer: "https://issuer", Subject: "subject-ada"}); e != nil {
		t.Fatal(e)
	}
	w = customerRequest(t, h, "GET", path, nil, false)
	if w.Code != 405 {
		t.Fatal(w.Code)
	}
	w = customerRequest(t, h, "POST", path, url.Values{}, false)
	if w.Code != 409 {
		t.Fatal("queued retry", w.Code)
	}
	pj, e := db.PaperlessRepository().Claim(ctx, time.Now().Add(time.Second), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.PaperlessRepository().Fail(ctx, pj, time.Now(), "configuration_missing", 1); e != nil {
		t.Fatal(e)
	}
	w = customerRequest(t, h, "GET", "/invoices/"+d.ID, nil, false)
	if !strings.Contains(w.Body.String(), "Paperless: failed") || !strings.Contains(w.Body.String(), "Configure delivery and retry") || !strings.Contains(w.Body.String(), path) {
		t.Fatal(w.Body.String())
	}
	authn.csrfErr = context.Canceled
	r := httptest.NewRequest("POST", "https://app.example.test"+path, strings.NewReader("csrf_token=bad"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("CSRF", w.Code)
	}
	authn.csrfErr = nil
	w = customerRequest(t, h, "POST", path, url.Values{}, true)
	if w.Code != 200 || w.Header().Get("HX-Redirect") != "/invoices/"+d.ID {
		t.Fatal(w.Code, w.Header())
	}
	authn.authenticateErr = context.Canceled
	w = customerRequest(t, h, "POST", path, url.Values{}, false)
	if w.Code != 302 {
		t.Fatal("anonymous", w.Code)
	}
}
