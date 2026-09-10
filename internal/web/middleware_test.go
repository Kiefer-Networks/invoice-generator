package web

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"log/slog"
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
	callbackErr, csrfErr, authenticateErr error
	callbackResult                        auth.SessionResult
	loggedOut                             bool
}

func (f *fakeAuth) Begin(string) (string, *http.Cookie, error) {
	return "https://pocket-id.test/authorize", &http.Cookie{Name: "__Host-invoice_oidc_transaction", Value: "state", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode}, nil
}
func (f *fakeAuth) Callback(context.Context, *url.URL, *http.Cookie) (auth.SessionResult, error) {
	if f.callbackErr != nil {
		result := f.callbackResult
		result.TransactionCookie = &http.Cookie{Name: "__Host-invoice_oidc_transaction", MaxAge: -1} // #nosec G124 -- Deliberately bare authentication fixture; the web boundary applies and tests response-cookie protections.
		return result, f.callbackErr
	}
	return auth.SessionResult{SessionCookie: &http.Cookie{Name: "__Host-invoice_session", Value: "session"}, TransactionCookie: &http.Cookie{Name: "__Host-invoice_oidc_transaction", MaxAge: -1}, ReturnTo: "/"}, nil // #nosec G124 -- Deliberately bare authentication fixture; the web boundary applies and tests response-cookie protections.
}

func TestRequestLogCapturesFinalResponseAndRecoveredPanicOnce(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		handler http.Handler
		status  int
		bytes   int
	}{
		{name: "response", handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte("created"))
		}), status: http.StatusCreated, bytes: len("created")},
		{name: "panic", handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") }), status: http.StatusInternalServerError, bytes: len("internal server error\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			a := &app{logger: slog.New(slog.NewJSONHandler(&output, nil))}
			h := a.log(a.recover(a.correlation(tc.handler)))
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://app.example.test/test", nil))
			if w.Code != tc.status || w.Body.Len() != tc.bytes {
				t.Fatalf("response status=%d bytes=%d, want %d/%d", w.Code, w.Body.Len(), tc.status, tc.bytes)
			}
			lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
			if len(lines) != 1 {
				t.Fatalf("request log count=%d, want 1: %q", len(lines), output.String())
			}
			var entry map[string]any
			if err := json.Unmarshal(lines[0], &entry); err != nil {
				t.Fatal(err)
			}
			if entry["status"] != float64(tc.status) || entry["bytes"] != float64(tc.bytes) {
				t.Fatalf("request log status/bytes=%v/%v, want %d/%d", entry["status"], entry["bytes"], tc.status, tc.bytes)
			}
			if entry["request_id"] == "" {
				t.Fatal("request log omitted correlation identifier")
			}
		})
	}
}
func (f *fakeAuth) Authenticate(context.Context, *http.Cookie) (auth.Principal, error) {
	return auth.Principal{UserID: "u1", Subject: "subject-ada", DisplayName: "Ada"}, f.authenticateErr
}

func TestAuthenticationDenialIgnoresHXHeaders(t *testing.T) {
	a := &fakeAuth{authenticateErr: context.DeadlineExceeded}
	h := testApp(t, a)
	for _, hx := range []bool{false, true} {
		r := httptest.NewRequest(http.MethodGet, "https://app.example.test/customers?x=1", nil)
		r.Host = "app.example.test"
		if hx {
			r.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusFound {
			t.Fatalf("HX=%t status=%d", hx, w.Code)
		}
		if !strings.Contains(w.Header().Get("Location"), "return_to=%2Fcustomers%3Fx%3D1") {
			t.Fatalf("unsafe return location %q", w.Header().Get("Location"))
		}
	}
}

func TestMissingAndExpiredSessionsAreDenied(t *testing.T) {
	for _, name := range []string{"missing", "expired"} {
		t.Run(name, func(t *testing.T) {
			a := &fakeAuth{authenticateErr: context.DeadlineExceeded}
			h := testApp(t, a)
			r := httptest.NewRequest(http.MethodGet, "https://app.example.test/", nil)
			r.Host = "app.example.test"
			if name == "expired" {
				r.AddCookie(&http.Cookie{Name: "__Host-invoice_session", Value: "expired"}) // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie.
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusFound {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
}

func TestLogoutRevokesAuthenticatedSession(t *testing.T) {
	a := &fakeAuth{}
	h := testApp(t, a)
	r := httptest.NewRequest(http.MethodPost, "https://app.example.test/auth/logout", strings.NewReader("csrf_token=csrf"))
	r.Host = "app.example.test"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "__Host-invoice_session", Value: "session"}) // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther || !a.loggedOut {
		t.Fatalf("logout status=%d revoked=%t", w.Code, a.loggedOut)
	}
}

func TestRootMethodIsExplicit(t *testing.T) {
	a := &fakeAuth{}
	h := testApp(t, a)
	r := httptest.NewRequest(http.MethodPost, "https://app.example.test/", strings.NewReader(""))
	r.Host = "app.example.test"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("X-CSRF-Token", "csrf")
	r.AddCookie(&http.Cookie{Name: "__Host-invoice_session", Value: "session"}) // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d", w.Code)
	}
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
	r.AddCookie(&http.Cookie{Name: "__Host-invoice_session", Value: "session"}) // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie.
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
			r.AddCookie(&http.Cookie{Name: "__Host-invoice_session", Value: "session"}) // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie. // #nosec G124 -- Request cookies carry only name/value; response-only security attributes are irrelevant to AddCookie.
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
	got, err := normalizeProxy(r, []netip.Prefix{p})
	if err != nil {
		t.Fatal(err)
	}
	if got.RemoteAddr != "198.51.100.8" {
		t.Fatalf("trusted forwarding was not normalized: %#v", got)
	}
	r.RemoteAddr = "203.0.113.7:443"
	got, err = normalizeProxy(r, []netip.Prefix{p})
	if err != nil {
		t.Fatal(err)
	}
	if got.RemoteAddr != "203.0.113.7:443" {
		t.Fatal("untrusted forwarded headers were accepted")
	}
}

func TestMiddlewareProxyChainUsesFirstUntrustedHop(t *testing.T) {
	prefixes := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("2001:db8::/32")}
	for _, tc := range []struct {
		name, chain, want string
		bad               bool
	}{
		{"client injected prefix", "203.0.113.99, 198.51.100.8, 10.0.0.2", "198.51.100.8", false},
		{"multiple trusted hops", "198.51.100.8, 10.0.0.3, 10.0.0.2", "198.51.100.8", false},
		{"ipv6", "2001:4860::1, 2001:db8::2", "2001:4860::1", false},
		{"malformed", "198.51.100.8, nope", "", true},
		{"empty", "198.51.100.8, ", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://internal/_health", nil)
			r.RemoteAddr = "10.0.0.2:443"
			r.Header.Set("X-Forwarded-For", tc.chain)
			r.Header.Set("X-Forwarded-Proto", "https")
			got, err := normalizeProxy(r, prefixes)
			if tc.bad {
				if err == nil {
					t.Fatal("accepted malformed forwarding chain")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.RemoteAddr != tc.want {
				t.Fatalf("client=%q want %q", got.RemoteAddr, tc.want)
			}
		})
	}
}

func TestMiddlewareProxyValidatesEveryForwardedField(t *testing.T) {
	prefixes := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}
	for _, tc := range []struct {
		name   string
		fields []string
		want   string
		bad    bool
	}{
		{"multiple field lines", []string{"203.0.113.66", "198.51.100.8, 10.0.0.2"}, "198.51.100.8", false},
		{"malformed left prefix", []string{"not-an-ip", "198.51.100.8, 10.0.0.2"}, "", true},
		{"empty element", []string{"203.0.113.66,, 10.0.0.2"}, "", true},
		{"all trusted", []string{"10.0.0.3, 10.0.0.2"}, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://internal/_health", nil)
			r.RemoteAddr = "10.0.0.2:443"
			r.Header["X-Forwarded-For"] = tc.fields
			got, err := normalizeProxy(r, prefixes)
			if tc.bad {
				if err == nil {
					t.Fatal("accepted unsafe chain")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got.RemoteAddr != tc.want {
				t.Fatalf("client=%q", got.RemoteAddr)
			}
		})
	}
}

func TestProductionTransportRequiresTLSOrTrustedHTTPSProxy(t *testing.T) {
	a := &fakeAuth{}
	h, err := New(Dependencies{Auth: a, Config: Config{AllowedHosts: []string{"app.example.test"}, TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}, BodyLimit: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, remote, proto string
		tls                 bool
		want                int
		wantHSTS            bool
	}{
		{"trusted https", "10.0.0.2:443", "https", false, http.StatusOK, true}, {"trusted http claim", "10.0.0.2:443", "http", false, http.StatusUpgradeRequired, false}, {"untrusted direct", "203.0.113.5:443", "https", false, http.StatusUpgradeRequired, false}, {"direct tls", "203.0.113.5:443", "", true, http.StatusOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "https://app.example.test/_health", nil)
			r.Host = "app.example.test"
			r.RemoteAddr = tc.remote
			r.Header.Set("X-Forwarded-Proto", tc.proto)
			r.TLS = nil
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d", w.Code)
			}
			if got := w.Header().Get("Strict-Transport-Security"); (got != "") != tc.wantHSTS {
				t.Fatalf("HSTS=%q, want present=%t", got, tc.wantHSTS)
			}
		})
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
		if c.Name == "__Host-invoice_session" {
			session = c
		}
	}
	if session == nil || session.SameSite != http.SameSiteStrictMode || !session.Secure || !session.HttpOnly {
		t.Fatal("application cookie is not protected with SameSite=Strict")
	}
	page := request(t, h, http.MethodGet, "/", "")
	for key, want := range map[string]string{"Cache-Control": "no-store", "Strict-Transport-Security": "max-age=63072000; includeSubDomains", "X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "strict-origin-when-cross-origin", "Permissions-Policy": "camera=(), geolocation=(), microphone=(), payment=()"} {
		if page.Header().Get(key) != want {
			t.Errorf("%s = %q, want %q", key, page.Header().Get(key), want)
		}
	}
	csp := page.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "'nonce-") || !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "unsafe-inline") {
		t.Fatalf("unsafe CSP: %q", csp)
	}
	if !strings.Contains(page.Body.String(), `"allowEval":false`) || !strings.Contains(page.Body.String(), `"includeIndicatorStyles":false`) {
		t.Fatal("HTMX unsafe defaults were not disabled")
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
