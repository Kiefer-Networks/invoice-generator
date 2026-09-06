package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestCompanyProfileRoutesSaveValidInputAndPreserveInvalidInput(t *testing.T) {
	t.Parallel()
	h, _ := customerApp(t, &fakeAuth{})

	page := customerRequest(t, h, http.MethodGet, "/settings/company", nil, false)
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Company profile") {
		t.Fatalf("company page = %d %q", page.Code, page.Body.String())
	}
	valid := url.Values{"legal_name": {"Kiefer Networks GmbH"}, "country": {"DE"}, "currency": {"EUR"}, "default_language": {"de"}, "brand_color": {"#5B9BD5"}, "payment_terms_days": {"14"}}
	saved := customerRequest(t, h, http.MethodPost, "/settings/company", valid, false)
	if saved.Code != http.StatusSeeOther || saved.Header().Get("Location") != "/settings/company" {
		t.Fatalf("save company = %d %q", saved.Code, saved.Header().Get("Location"))
	}
	invalid := url.Values{"legal_name": {"Kiefer Networks GmbH"}, "country": {"DE"}, "currency": {"EUR"}, "default_language": {"de"}, "brand_color": {"not-a-color"}, "payment_terms_days": {"14"}}
	bad := customerRequest(t, h, http.MethodPost, "/settings/company", invalid, false)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "not-a-color") || !strings.Contains(bad.Body.String(), "brand_color") {
		t.Fatalf("invalid company response = %d %q", bad.Code, bad.Body.String())
	}
}

func customerApp(t *testing.T, authn *fakeAuth) (http.Handler, *store.Store) {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	h, err := New(Dependencies{Auth: authn, Store: s, Config: Config{AllowedHosts: []string{"app.example.test"}, BodyLimit: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	return h, s
}

func customerRequest(t *testing.T, h http.Handler, method, target string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if form == nil {
		body = strings.NewReader("")
	} else {
		form.Set("csrf_token", "csrf")
		body = strings.NewReader(form.Encode())
	}
	r := httptest.NewRequest(method, "https://app.example.test"+target, body)
	r.Host = "app.example.test"
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if htmx {
		r.Header.Set("HX-Request", "true")
	}
	r.AddCookie(&http.Cookie{Name: "invoice_session", Value: "session"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
