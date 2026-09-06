package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func customerForm(number, name string) url.Values {
	return url.Values{"number": {number}, "display_name": {name}, "country": {"DE"}, "preferred_language": {"de"}, "currency": {"EUR"}, "payment_terms_days": {"14"}}
}

func TestCustomerRoutesRenderMasterDetailAndCRUD(t *testing.T) {
	t.Parallel()
	h, _ := customerApp(t, &fakeAuth{})
	list := customerRequest(t, h, http.MethodGet, "/customers", nil, false)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), "Customers") || !strings.Contains(list.Body.String(), "customer-detail") {
		t.Fatalf("list = %d %q", list.Code, list.Body.String())
	}
	newPage := customerRequest(t, h, http.MethodGet, "/customers/new", nil, false)
	if newPage.Code != http.StatusOK || !strings.Contains(newPage.Body.String(), "New customer") {
		t.Fatalf("new page = %d %q", newPage.Code, newPage.Body.String())
	}
	created := customerRequest(t, h, http.MethodPost, "/customers/new", customerForm("C-001", "Acme GmbH"), false)
	if created.Code != http.StatusSeeOther || !strings.HasPrefix(created.Header().Get("Location"), "/customers/") {
		t.Fatalf("create = %d %q", created.Code, created.Header().Get("Location"))
	}
	location := created.Header().Get("Location")
	detail := customerRequest(t, h, http.MethodGet, location, nil, false)
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), "Acme GmbH") || !strings.Contains(detail.Body.String(), "Archive customer") {
		t.Fatalf("detail = %d %q", detail.Code, detail.Body.String())
	}
	edit := customerRequest(t, h, http.MethodGet, location+"/edit", nil, false)
	if edit.Code != http.StatusOK || !strings.Contains(edit.Body.String(), "Edit customer") {
		t.Fatalf("edit = %d %q", edit.Code, edit.Body.String())
	}
	id := strings.TrimPrefix(location, "/customers/")
	update := customerForm("C-001", "Acme Updated")
	update.Set("version", "1")
	updated := customerRequest(t, h, http.MethodPost, location+"/edit", update, true)
	if updated.Code != http.StatusOK || updated.Header().Get("HX-Retarget") != "#customer-detail" || !strings.Contains(updated.Body.String(), "Acme Updated") {
		t.Fatalf("HTMX update = %d target=%q body=%q", updated.Code, updated.Header().Get("HX-Retarget"), updated.Body.String())
	}
	archive := url.Values{"version": {"2"}}
	archived := customerRequest(t, h, http.MethodPost, "/customers/"+id+"/archive", archive, false)
	if archived.Code != http.StatusSeeOther || archived.Header().Get("Location") != location {
		t.Fatalf("archive = %d %q", archived.Code, archived.Header().Get("Location"))
	}
	archivedDetail := customerRequest(t, h, http.MethodGet, location, nil, false)
	if archivedDetail.Code != http.StatusOK || !strings.Contains(archivedDetail.Body.String(), "Restore customer") {
		t.Fatalf("archived detail = %d %q", archivedDetail.Code, archivedDetail.Body.String())
	}
	restored := customerRequest(t, h, http.MethodPost, "/customers/"+id+"/restore", url.Values{"version": {"3"}}, false)
	if restored.Code != http.StatusSeeOther {
		t.Fatalf("restore = %d", restored.Code)
	}
}

func TestCustomerRoutesRejectInvalidDuplicateAndStaleInput(t *testing.T) {
	t.Parallel()
	h, s := customerApp(t, &fakeAuth{})
	invalid := customerForm("C-001", "Acme")
	invalid.Set("email", "invalid")
	invalid.Set("country", "D")
	invalid.Set("currency", "EURO")
	invalid.Set("notes", strings.Repeat("x", 5001))
	bad := customerRequest(t, h, http.MethodPost, "/customers/new", invalid, false)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "invalid") || !strings.Contains(bad.Body.String(), "C-001") {
		t.Fatalf("invalid customer = %d %q", bad.Code, bad.Body.String())
	}
	created, err := s.CustomerRepository().Create(context.Background(), store.CustomerInput{Number: "C-001", DisplayName: "Acme", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14})
	if err != nil {
		t.Fatal(err)
	}
	duplicate := customerRequest(t, h, http.MethodPost, "/customers/new", customerForm("C-001", "Other"), false)
	if duplicate.Code != http.StatusBadRequest || !strings.Contains(duplicate.Body.String(), "already exists") {
		t.Fatalf("duplicate = %d %q", duplicate.Code, duplicate.Body.String())
	}
	if _, err := s.CustomerRepository().Update(context.Background(), created.ID, created.Version, store.CustomerInput{Number: "C-001", DisplayName: "Current", Country: "DE", PreferredLanguage: "de", Currency: "EUR", PaymentTermsDays: 14}); err != nil {
		t.Fatal(err)
	}
	stale := customerForm("C-001", "Stale")
	stale.Set("version", strconv.Itoa(created.Version))
	conflict := customerRequest(t, h, http.MethodPost, "/customers/"+created.ID+"/edit", stale, false)
	if conflict.Code != http.StatusConflict || !strings.Contains(conflict.Body.String(), "changed") {
		t.Fatalf("stale update = %d %q", conflict.Code, conflict.Body.String())
	}
}

func TestCustomerRoutesRequireCSRFAndAuthentication(t *testing.T) {
	t.Parallel()
	csrf := &fakeAuth{csrfErr: context.Canceled}
	h, _ := customerApp(t, csrf)
	forbidden := customerRequest(t, h, http.MethodPost, "/customers/new", customerForm("C-001", "Acme"), false)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("CSRF status=%d", forbidden.Code)
	}
	unauthenticated, _ := customerApp(t, &fakeAuth{authenticateErr: context.Canceled})
	denied := customerRequest(t, unauthenticated, http.MethodGet, "/customers", nil, false)
	if denied.Code != http.StatusFound || !strings.Contains(denied.Header().Get("Location"), "/auth/login") {
		t.Fatalf("authorization = %d %q", denied.Code, denied.Header().Get("Location"))
	}
}
