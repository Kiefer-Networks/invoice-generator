package web

import (
	"bytes"
	"errors"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var documentIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{20,64}$`)

func documentHeaders(w http.ResponseWriter, disposition string) {
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Disposition", disposition+"; filename=invoice.pdf")
}
func (a *app) documentRoute(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/documents/"), "/")
	if len(parts) != 2 || !documentIDPattern.MatchString(parts[0]) || (parts[1] != "download" && parts[1] != "retry" && parts[1] != "paperless-retry") {
		http.NotFound(w, r)
		return
	}
	if a.documents == nil || a.store == nil {
		http.Error(w, "document service unavailable", 503)
		return
	}
	id := parts[0]
	if parts[1] == "retry" || parts[1] == "paperless-retry" {
		if r.Method != "POST" {
			methodNotAllowed(w, "POST")
			return
		}
		p, _ := principalFromContext(r.Context())
		var active int
		if e := a.store.DB().QueryRowContext(r.Context(), `SELECT 1 FROM oidc_users WHERE id=? AND active=1`, p.UserID).Scan(&active); e != nil {
			http.NotFound(w, r)
			return
		}
		d, e := a.store.DocumentRepository().Get(r.Context(), id)
		if e != nil {
			http.NotFound(w, r)
			return
		}
		if parts[1] == "paperless-retry" {
			requestID, _ := r.Context().Value(requestIDKey).(string)
			e = a.store.PaperlessRepository().Retry(store.WithInvoiceAudit(r.Context(), p.Subject, requestID), id)
		} else {
			e = a.store.DocumentRepository().Retry(r.Context(), id)
		}
		if e != nil {
			http.Error(w, "job is already queued or completed", 409)
			return
		}
		if a.wakeDocuments != nil {
			a.wakeDocuments()
		}
		path := "/invoices/" + d.InvoiceID
		if isHTMX(r) {
			w.Header().Set("HX-Redirect", path)
			w.WriteHeader(200)
		} else {
			http.Redirect(w, r, path, 303)
		}
		return
	}
	if r.Method != "GET" && r.Method != "HEAD" {
		methodNotAllowed(w, "GET, HEAD")
		return
	}
	p, _ := principalFromContext(r.Context())
	f, d, e := a.documents.OpenAuthorized(r.Context(), p.UserID, id)
	if e != nil {
		if errors.Is(e, store.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "document integrity check failed", 500)
		}
		return
	}
	defer f.Close()
	documentHeaders(w, "attachment")
	w.Header().Set("ETag", `"`+d.SHA256+`"`)
	http.ServeContent(w, r, "invoice.pdf", time.Time{}, f)
}
func (a *app) invoicePDFPreview(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != "GET" {
		methodNotAllowed(w, "GET")
		return
	}
	if !documentIDPattern.MatchString(id) {
		http.NotFound(w, r)
		return
	}
	if a.documents == nil || a.store == nil {
		http.Error(w, "document service unavailable", 503)
		return
	}
	p, _ := principalFromContext(r.Context())
	var active int
	if e := a.store.DB().QueryRowContext(r.Context(), `SELECT 1 FROM oidc_users WHERE id=? AND active=1`, p.UserID).Scan(&active); e != nil {
		http.NotFound(w, r)
		return
	}
	data, e := a.documents.Preview(r.Context(), id)
	if e != nil {
		if errors.Is(e, store.ErrNotFound) {
			http.NotFound(w, r)
		} else {
			http.Error(w, "preview generation unavailable; retry later", 503)
		}
		return
	}
	documentHeaders(w, "inline")
	http.ServeContent(w, r, "invoice.pdf", time.Time{}, bytes.NewReader(data))
}
