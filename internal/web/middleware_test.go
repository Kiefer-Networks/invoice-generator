package web

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/auth"
)

type fakeAuth struct {
	callbackErr, csrfErr error
	loggedOut            bool
}

func (f *fakeAuth) Begin(string) (string, *http.Cookie, error) {
	return "https://pocket-id.test/authorize", &http.Cookie{Name: "invoice_oidc_transaction", Value: "state", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}, nil
}
func (f *fakeAuth) Callback(context.Context, *url.URL, *http.Cookie) (auth.SessionResult, error) {
	if f.callbackErr != nil {
		return auth.SessionResult{TransactionCookie: &http.Cookie{Name: "invoice_oidc_transaction", MaxAge: -1}}, f.callbackErr
	}
	return auth.SessionResult{SessionCookie: &http.Cookie{Name: "invoice_session", Value: "session"}, TransactionCookie: &http.Cookie{Name: "invoice_oidc_transaction", MaxAge: -1}, ReturnTo: "/"}, nil
}
func (f *fakeAuth) Authenticate(context.Context, *http.Cookie) (auth.Principal, error) {
	return auth.Principal{UserID: "u1", DisplayName: "Ada"}, nil
}
func (f *fakeAuth) ValidateCSRF(context.Context, *http.Cookie, string) error { return f.csrfErr }
func (f *fakeAuth) CSRFToken(*http.Cookie) (string, error)                   { return "csrf", nil }
func (f *fakeAuth) Logout(context.Context, *http.Cookie) error               { f.loggedOut = true; return nil }

func testApp(t *testing.T, a *fakeAuth) http.Handler {
	t.Helper()
	h, err := New(Dependencies{Auth: a, Config: Config{AllowedHosts: []string{"app.example.test"}, BodyLimit: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func request(t *testing.T, h http.Handler, method, target string, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "https://app.example.test"+target, strings.NewReader(body))
	r.Host = "app.example.test"
	r.AddCookie(&http.Cookie{Name: "invoice_session", Value: "session"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestMiddleware(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, method, target, body string
		setup                      func(*http.Request)
		want                       int
	}{
		{"correlation ID", http.MethodGet, "/_health", "", nil, http.StatusOK},
		{"unknown host", http.MethodGet, "/_health", "", func(r *http.Request) { r.Host = "evil.test" }, http.StatusMisdirectedRequest},
		{"login only GET", http.MethodPost, "/auth/login", "", nil, http.StatusMethodNotAllowed},
		{"logout content type", http.MethodPost, "/auth/logout", "{}", func(r *http.Request) {
			r.Header.Set("Content-Type", "application/json")
			r.Header.Set("X-CSRF-Token", "csrf")
		}, http.StatusUnsupportedMediaType},
		{"csrf rejection", http.MethodPost, "/auth/logout", "", func(r *http.Request) { r.Header.Set("Content-Type", "application/x-www-form-urlencoded") }, http.StatusForbidden},
		{"form limit", http.MethodPost, "/auth/logout", strings.Repeat("a", (1<<20)+1), func(r *http.Request) {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("X-CSRF-Token", "csrf")
		}, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &fakeAuth{}
			if tc.name == "csrf rejection" {
				a.csrfErr = context.Canceled
			}
			h := testApp(t, a)
			r := httptest.NewRequest(tc.method, "https://app.example.test"+tc.target, strings.NewReader(tc.body))
			r.Host = "app.example.test"
			r.AddCookie(&http.Cookie{Name: "invoice_session", Value: "session"})
			if tc.setup != nil {
				tc.setup(r)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			if tc.name == "correlation ID" && w.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing request correlation ID")
			}
		})
	}
}

func TestMiddlewareTrustedProxyNormalization(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://internal/_health", nil)
	r.RemoteAddr = "10.0.0.2:443"
	r.Header.Set("X-Forwarded-For", "198.51.100.8, 10.0.0.2")
	r.Header.Set("X-Forwarded-Proto", "https")
	p := netip.MustParsePrefix("10.0.0.0/24")
	got := normalizeProxy(r, []netip.Prefix{p})
	if got.RemoteAddr != "198.51.100.8" || got.URL.Scheme != "https" {
		t.Fatalf("trusted forwarding was not normalized: %#v", got)
	}
	r.RemoteAddr = "203.0.113.7:443"
	got = normalizeProxy(r, []netip.Prefix{p})
	if got.URL.Scheme == "https" {
		t.Fatal("untrusted forwarded headers were accepted")
	}
}

func TestSecurityHeadersAndAuthenticationFlow(t *testing.T) {
	a := &fakeAuth{}
	h := testApp(t, a)
	login := request(t, h, http.MethodGet, "/auth/login?return_to=/customers", "")
	if login.Code != http.StatusFound || login.Header().Get("Location") != "https://pocket-id.test/authorize" {
		t.Fatalf("login = %d %q", login.Code, login.Header().Get("Location"))
	}
	transaction := login.Result().Cookies()[0]
	if transaction.SameSite != http.SameSiteLaxMode || !transaction.Secure || !transaction.HttpOnly {
		t.Fatal("transaction cookie is not protected with SameSite=Lax")
	}
	callbackReq := httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/callback?code=x&state=y", nil)
	callbackReq.Host = "app.example.test"
	callbackReq.AddCookie(transaction)
	callback := httptest.NewRecorder()
	h.ServeHTTP(callback, callbackReq)
	if callback.Code != http.StatusFound {
		t.Fatalf("callback = %d", callback.Code)
	}
	var session *http.Cookie
	for _, c := range callback.Result().Cookies() {
		if c.Name == "invoice_session" {
			session = c
		}
	}
	if session == nil || session.SameSite != http.SameSiteStrictMode || !session.Secure || !session.HttpOnly {
		t.Fatal("application cookie is not protected with SameSite=Strict")
	}
	page := request(t, h, http.MethodGet, "/", "")
	for key, want := range map[string]string{"Cache-Control": "no-store", "X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "strict-origin-when-cross-origin", "Permissions-Policy": "camera=(), geolocation=(), microphone=(), payment=()"} {
		if page.Header().Get(key) != want {
			t.Errorf("%s = %q, want %q", key, page.Header().Get(key), want)
		}
	}
	csp := page.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "'nonce-") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("unsafe CSP: %q", csp)
	}
}

func TestCallbackFailureClearsTransaction(t *testing.T) {
	a := &fakeAuth{callbackErr: context.Canceled}
	h := testApp(t, a)
	r := httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/callback?error=access_denied", nil)
	r.Host = "app.example.test"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
	if len(w.Result().Cookies()) == 0 || w.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatal("callback failure did not clear the transaction")
	}
}

var _ = net.IP{}
