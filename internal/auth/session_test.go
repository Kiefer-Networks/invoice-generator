package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestCookieNamesSeparateSecureAndLoopbackDevelopment(t *testing.T) {
	if got := SessionCookieNameForSecure(true); got != "__Host-invoice_session" {
		t.Fatalf("secure session cookie = %q", got)
	}
	if got := SessionCookieNameForSecure(false); got != "invoice_session_dev" {
		t.Fatalf("development session cookie = %q", got)
	}
	if got := TransactionCookieNameForSecure(true); got != "__Host-invoice_oidc_transaction" {
		t.Fatalf("secure transaction cookie = %q", got)
	}
	if got := TransactionCookieNameForSecure(false); got != "invoice_oidc_transaction_dev" {
		t.Fatalf("development transaction cookie = %q", got)
	}
	if SessionCookieNameForSecure(false) == SessionCookieNameForSecure(true) {
		t.Fatal("development reused production cookie name")
	}
}

func TestManagerEnforcesConcurrentSessionLimitAndCanManageSessions(t *testing.T) {
	provider := newTestProvider(t)
	manager := newTestManager(t, provider, testStore(t))
	var latest SessionResult
	for i := 0; i < maxConcurrentSessions; i++ {
		latest = completeLogin(t, manager, provider)
	}
	redirect, transaction, err := manager.Begin("/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	provider.setNonce(u.Query().Get("nonce"))
	callback, _ := url.Parse("https://app.example.test/auth/callback?code=code-extra&state=" + u.Query().Get("state"))
	if _, err = manager.Callback(context.Background(), callback, transaction); !errors.Is(err, store.ErrSessionLimit) {
		t.Fatalf("extra session error = %v", err)
	}
	sessions, err := manager.ListSessions(context.Background(), latest.SessionCookie)
	if err != nil || len(sessions) != maxConcurrentSessions {
		t.Fatalf("sessions=%d err=%v", len(sessions), err)
	}
	current := 0
	for _, session := range sessions {
		if session.Current {
			current++
		}
	}
	if current != 1 {
		t.Fatalf("current markers=%d", current)
	}
	for _, session := range sessions {
		if !session.Current {
			if err := manager.RevokeSession(context.Background(), latest.SessionCookie, session.ID); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if _, err := manager.Authenticate(context.Background(), &http.Cookie{Name: latest.SessionCookie.Name, Value: latest.SessionCookie.Value}); err != nil {
		t.Fatal(err)
	}
}

func TestSessionLifecycleRotatesRevokesAndBindsCSRF(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	manager := newTestManager(t, provider, testStore(t))

	first := completeLogin(t, manager, provider)
	if _, err := manager.Authenticate(context.Background(), first.SessionCookie); err != nil {
		t.Fatalf("Authenticate first session: %v", err)
	}
	if err := manager.ValidateCSRF(context.Background(), first.SessionCookie, first.CSRFToken); err != nil {
		t.Fatalf("ValidateCSRF: %v", err)
	}
	if err := manager.ValidateCSRF(context.Background(), first.SessionCookie, "attacker-token"); err == nil {
		t.Fatal("ValidateCSRF accepted a token from another session")
	}

	second := completeLogin(t, manager, provider)
	if first.SessionCookie.Value == second.SessionCookie.Value {
		t.Fatal("login reused the local session token")
	}
	if err := manager.Logout(context.Background(), first.SessionCookie); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(context.Background(), first.SessionCookie); err == nil {
		t.Fatal("logged-out session authenticated")
	}
	principal, err := manager.Authenticate(context.Background(), second.SessionCookie)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RevokeAll(context.Background(), principal); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Authenticate(context.Background(), second.SessionCookie); err == nil {
		t.Fatal("globally revoked session authenticated")
	}
}

func TestSessionExpiresFifteenMinutesAfterVerifiedLogin(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	manager := newTestManager(t, provider, testStore(t))
	result := completeLogin(t, manager, provider)
	manager.now = func() time.Time { return time.Now().Add(16 * time.Minute) }
	if _, err := manager.Authenticate(context.Background(), result.SessionCookie); err == nil {
		t.Fatal("session authenticated after its fifteen-minute lifetime")
	}
}

func TestCallbackDeniesIdentityOutsideInvoiceAdmins(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	provider.setGroups([]string{"billing"})
	manager := newTestManager(t, provider, testStore(t))
	redirect, cookie, err := manager.Begin("/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	provider.setNonce(u.Query().Get("nonce"))
	callback, _ := url.Parse("https://app.example.test/auth/callback?code=code-1&state=" + u.Query().Get("state"))
	result, err := manager.Callback(context.Background(), callback, cookie)
	if err == nil {
		t.Fatal("callback accepted an identity outside invoice-admins")
	}
	if result.AuditSubject == "" || strings.Contains(result.AuditSubject, "person-1") || strings.Contains(result.AuditSubject, "person@example.test") {
		t.Fatalf("denied identity has unsafe audit subject %q", result.AuditSubject)
	}
	redirect, cookie, err = manager.Begin("/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ = url.Parse(redirect)
	provider.setNonce(u.Query().Get("nonce"))
	callback, _ = url.Parse("https://app.example.test/auth/callback?code=code-2&state=" + u.Query().Get("state"))
	second, err := manager.Callback(context.Background(), callback, cookie)
	if err == nil || second.AuditSubject != result.AuditSubject {
		t.Fatalf("denied identity audit subject is not stable: first=%q second=%q err=%v", result.AuditSubject, second.AuditSubject, err)
	}
}

func completeLogin(t *testing.T, manager *Manager, provider *testProvider) SessionResult {
	t.Helper()
	redirect, cookie, err := manager.Begin("/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	provider.setNonce(u.Query().Get("nonce"))
	callback, _ := url.Parse("https://app.example.test/auth/callback?code=code-1&state=" + u.Query().Get("state"))
	result, err := manager.Callback(context.Background(), callback, cookie)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
