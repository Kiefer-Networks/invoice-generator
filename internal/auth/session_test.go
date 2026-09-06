package auth

import (
	"context"
	"net/url"
	"testing"
	"time"
)

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
	if _, err := manager.Callback(context.Background(), callback, cookie); err == nil {
		t.Fatal("callback accepted an identity outside invoice-admins")
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
