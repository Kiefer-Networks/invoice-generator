package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/auth"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

type auditAuth struct{ *fakeAuth }

func (a *auditAuth) Callback(ctx context.Context, callback *url.URL, transaction *http.Cookie) (auth.SessionResult, error) {
	result, err := a.fakeAuth.Callback(ctx, callback, transaction)
	result.Principal = auth.Principal{UserID: "u1", Subject: "subject-ada", DisplayName: "Ada"}
	return result, err
}

func auditTestApp(t *testing.T, authn Authenticator, config Config) (http.Handler, *store.Store) {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "audit-web.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err = db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	config.AllowedHosts = []string{"app.example.test"}
	h, err := New(Dependencies{Auth: authn, Store: db, Config: config})
	if err != nil {
		t.Fatal(err)
	}
	return h, db
}

func TestAuthenticationOutcomesAreDurablyAudited(t *testing.T) {
	t.Parallel()
	authn := &auditAuth{fakeAuth: &fakeAuth{}}
	h, db := auditTestApp(t, authn, Config{})

	login := httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/login", nil)
	login.Host = "app.example.test"
	h.ServeHTTP(httptest.NewRecorder(), login)

	callback := httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/callback?code=x&state=y", nil)
	callback.Host = "app.example.test"
	h.ServeHTTP(httptest.NewRecorder(), callback)

	logout := httptest.NewRequest(http.MethodPost, "https://app.example.test/auth/logout", strings.NewReader("csrf_token=csrf"))
	logout.Host = "app.example.test"
	logout.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	logout.AddCookie(&http.Cookie{Name: "__Host-invoice_session", Value: "session"}) // #nosec G124 -- Request-only fixture; response cookie attributes do not apply to AddCookie.
	h.ServeHTTP(httptest.NewRecorder(), logout)

	rows, err := db.DB().Query(`SELECT action,result,actor_subject,change_summary FROM audit_events ORDER BY created_at,id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string][3]string{}
	for rows.Next() {
		var action, result, actor, summary string
		if err := rows.Scan(&action, &result, &actor, &summary); err != nil {
			t.Fatal(err)
		}
		got[action] = [3]string{result, actor, summary}
	}
	if got["auth.login"] != [3]string{"success", "", "{}"} || got["auth.callback"] != [3]string{"success", "subject-ada", "{}"} || got["auth.logout"] != [3]string{"success", "subject-ada", "{}"} {
		t.Fatalf("unexpected auth audits: %#v", got)
	}
}

func TestAuthenticationFailureIsAuditedWithoutErrorDetails(t *testing.T) {
	t.Parallel()
	authn := &fakeAuth{callbackErr: errors.New("token secret must never be logged")}
	h, db := auditTestApp(t, authn, Config{})
	r := httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/callback?code=secret&state=secret", nil)
	r.Host = "app.example.test"
	h.ServeHTTP(httptest.NewRecorder(), r)
	var result, summary string
	if err := db.DB().QueryRow(`SELECT result,change_summary FROM audit_events WHERE action='auth.callback'`).Scan(&result, &summary); err != nil {
		t.Fatal(err)
	}
	if result != "failure" || summary != "{}" {
		t.Fatalf("failure audit leaked detail: result=%q summary=%q", result, summary)
	}
}

func TestDeniedGroupCallbackUsesOnlyPseudonymousAuditSubject(t *testing.T) {
	t.Parallel()
	want := "oidc-subject:v1:opaque"
	authn := &fakeAuth{callbackErr: errors.New("identity is not authorized"), callbackResult: auth.SessionResult{AuditSubject: want}}
	h, db := auditTestApp(t, authn, Config{})
	r := httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/callback?code=secret&state=secret", nil)
	r.Host = "app.example.test"
	h.ServeHTTP(httptest.NewRecorder(), r)
	var actor string
	if err := db.DB().QueryRow(`SELECT actor_subject FROM audit_events WHERE action='auth.callback'`).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if actor != want {
		t.Fatalf("callback failure actor=%q, want pseudonymous subject %q", actor, want)
	}
}

func TestConfigurationCRUDWritesActorAndObjectAudits(t *testing.T) {
	t.Parallel()
	h, db := auditTestApp(t, &fakeAuth{}, Config{})
	form := url.Values{"number": {"C-1"}, "display_name": {"Buyer"}, "country": {"DE"}, "preferred_language": {"de"}, "currency": {"EUR"}, "payment_terms_days": {"14"}}
	r := httptest.NewRequest(http.MethodPost, "https://app.example.test/customers/new", strings.NewReader(form.Encode()+"&csrf_token=csrf"))
	r.Host = "app.example.test"
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.AddCookie(&http.Cookie{Name: "__Host-invoice_session", Value: "session"}) // #nosec G124 -- Request-only fixture; response cookie attributes do not apply to AddCookie.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create status=%d body=%q", w.Code, w.Body.String())
	}
	var actor, targetID, requestID, summary string
	if err := db.DB().QueryRow(`SELECT actor_subject,target_id,request_id,change_summary FROM audit_events WHERE action='customer.created'`).Scan(&actor, &targetID, &requestID, &summary); err != nil {
		t.Fatal(err)
	}
	if actor != "subject-ada" || targetID == "" || requestID == "" || summary != "{}" {
		t.Fatalf("unsafe or incomplete CRUD audit: actor=%q target=%q request=%q summary=%q", actor, targetID, requestID, summary)
	}
}

func TestAuthRateLimitUsesNormalizedClientAddress(t *testing.T) {
	t.Parallel()
	now := time.Unix(1_800_000_000, 0)
	h, _ := auditTestApp(t, &fakeAuth{}, Config{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}, authRateLimit: 2, authRateWindow: time.Minute, now: func() time.Time { return now }})
	request := func(client string) int {
		r := httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/login", nil)
		r.Host = "app.example.test"
		r.RemoteAddr = "10.0.0.8:1234"
		r.Header.Set("X-Forwarded-For", client)
		r.Header.Set("X-Forwarded-Proto", "https")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	first := request("198.51.100.1")
	second := request("198.51.100.1")
	third := request("198.51.100.1")
	if first != http.StatusFound || second != http.StatusFound || third != http.StatusTooManyRequests {
		t.Fatalf("same normalized client statuses = %d, %d, %d", first, second, third)
	}
	if got := request("198.51.100.2"); got != http.StatusFound {
		t.Fatalf("different client shared rate bucket: %d", got)
	}
}

func TestUnauthenticatedLogoutDoesNotConsumeLoginRateBudget(t *testing.T) {
	t.Parallel()
	h, _ := auditTestApp(t, &fakeAuth{}, Config{authRateLimit: 1, authRateWindow: time.Minute})
	for i := 0; i < 3; i++ {
		r := httptest.NewRequest(http.MethodPost, "https://app.example.test/auth/logout", nil)
		r.Host = "app.example.test"
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	login := httptest.NewRequest(http.MethodGet, "https://app.example.test/auth/login", nil)
	login.Host = "app.example.test"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, login)
	if w.Code != http.StatusFound {
		t.Fatalf("login status=%d after invalid logout requests, want %d", w.Code, http.StatusFound)
	}
}

func TestExpensiveOperationLimitFailsClosedWithoutQueueing(t *testing.T) {
	t.Parallel()
	a := &app{expensive: make(chan struct{}, 1)}
	entered := make(chan struct{})
	release := make(chan struct{})
	h := a.resources(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	firstDone := make(chan int, 1)
	go func() {
		r := httptest.NewRequest(http.MethodPost, "https://app.example.test/invoices/one/finalize", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		firstDone <- w.Code
	}()
	<-entered
	second := httptest.NewRecorder()
	h.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "https://app.example.test/invoices/two/preview", nil))
	if second.Code != http.StatusServiceUnavailable || second.Header().Get("Retry-After") == "" {
		t.Fatalf("second expensive request=%d retry=%q", second.Code, second.Header().Get("Retry-After"))
	}
	close(release)
	if got := <-firstDone; got != http.StatusNoContent {
		t.Fatalf("first request=%d", got)
	}
}

func TestClientLimiterStateIsBounded(t *testing.T) {
	t.Parallel()
	limiter := newClientRateLimiter(2, time.Minute, 4, time.Now)
	for i := 0; i < 100; i++ {
		limiter.allow(string(rune(i + 1)))
	}
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	if len(limiter.clients) > 4 {
		t.Fatalf("client limiter grew to %d", len(limiter.clients))
	}
}
