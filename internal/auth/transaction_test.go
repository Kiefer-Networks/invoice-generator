package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestAuthorizationTransactionUsesPKCEAndSafeReturnPath(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	manager := newTestManager(t, provider, testStore(t))

	redirect, cookie, err := manager.Begin("https://attacker.example.test/")
	if err != nil {
		t.Fatal(err)
	}
	if cookie == nil || cookie.Name != transactionCookieName || cookie.MaxAge <= 0 || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unsafe transaction cookie: %#v", cookie)
	}
	u, err := url.Parse(redirect)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("state") == "" || q.Get("nonce") == "" {
		t.Fatalf("authorization URL misses PKCE or transaction values: %s", redirect)
	}
	provider.setNonce(q.Get("nonce"))
	state := q.Get("state")
	callback, _ := url.Parse("https://app.example.test/auth/callback?code=code-1&state=" + url.QueryEscape(state))
	result, err := manager.Callback(context.Background(), callback, cookie)
	if err != nil {
		t.Fatal(err)
	}
	if result.ReturnTo != "/" {
		t.Fatalf("ReturnTo=%q, want /", result.ReturnTo)
	}
	if result.TransactionCookie == nil || result.TransactionCookie.MaxAge >= 0 {
		t.Fatal("successful callback did not delete the transaction cookie")
	}
}

func TestAuthorizationTransactionRejectsTamperingAndReplayBeforeExchange(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	manager := newTestManager(t, provider, testStore(t))
	redirect, cookie, err := manager.Begin("/invoices")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	state := u.Query().Get("state")
	provider.setNonce(u.Query().Get("nonce"))
	callback, _ := url.Parse("https://app.example.test/auth/callback?code=code-1&state=" + state)

	tampered := *cookie
	rawCookie, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	rawCookie[len(rawCookie)-1] ^= 1
	tampered.Value = base64.RawURLEncoding.EncodeToString(rawCookie)
	if _, err := manager.Callback(context.Background(), callback, &tampered); err == nil {
		t.Fatal("tampered transaction cookie was accepted")
	}
	result, err := manager.Callback(context.Background(), callback, cookie)
	if err != nil {
		t.Fatal(err)
	}
	if result.SessionCookie == nil {
		t.Fatal("callback did not create a session")
	}
	if _, err := manager.Callback(context.Background(), callback, cookie); err == nil {
		t.Fatal("replayed transaction was accepted")
	}
	if provider.exchanges() != 1 {
		t.Fatalf("token exchanges=%d, want 1", provider.exchanges())
	}
}

func TestAuthorizationTransactionRejectsNonCanonicalCookieWithoutConsumingTransaction(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	manager := newTestManager(t, provider, testStore(t))
	now := time.Now().UTC().Truncate(time.Second).Add(123456789 * time.Nanosecond)
	manager.now = func() time.Time { return now }
	redirect, cookie, err := manager.Begin("/invoices")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	provider.setNonce(u.Query().Get("nonce"))
	callback, _ := url.Parse("https://app.example.test/auth/callback?code=code-1&state=" + u.Query().Get("state"))

	nonCanonical := *cookie
	nonCanonical.Value = alternateRawURLSpelling(t, cookie.Value)
	if _, err := manager.Callback(context.Background(), callback, &nonCanonical); err == nil {
		t.Fatal("noncanonical transaction cookie was accepted")
	}
	if provider.exchanges() != 0 {
		t.Fatal("noncanonical transaction cookie reached token endpoint")
	}
	if _, err := manager.Callback(context.Background(), callback, cookie); err != nil {
		t.Fatalf("canonical transaction cookie was consumed by rejected alias: %v", err)
	}
	if provider.exchanges() != 1 {
		t.Fatalf("token exchanges=%d, want 1", provider.exchanges())
	}
}

func alternateRawURLSpelling(t *testing.T, value string) string {
	t.Helper()
	if remainder := len(value) % 4; remainder != 2 && remainder != 3 {
		t.Fatalf("test transaction cookie has no unused base64 bits: encoded length %% 4 = %d", remainder)
	}
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	index := strings.IndexByte(alphabet, value[len(value)-1])
	if index < 0 {
		t.Fatal("test transaction cookie is not raw URL base64")
	}
	alternate := value[:len(value)-1] + string(alphabet[index^1])
	originalBytes, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatal(err)
	}
	alternateBytes, err := base64.RawURLEncoding.DecodeString(alternate)
	if err != nil {
		t.Fatal(err)
	}
	if alternate == value || string(alternateBytes) != string(originalBytes) {
		t.Fatal("alternate spelling does not decode to the original transaction bytes")
	}
	return alternate
}

func TestAuthorizationTransactionRejectsDuplicateStateAndOAuthError(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	manager := newTestManager(t, provider, testStore(t))
	redirect, cookie, err := manager.Begin("/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	state := u.Query().Get("state")
	for _, raw := range []string{
		"https://app.example.test/auth/callback?code=code-1&state=" + state + "&state=" + state,
		"https://app.example.test/auth/callback?error=access_denied&state=" + state,
	} {
		callback, _ := url.Parse(raw)
		if _, err := manager.Callback(context.Background(), callback, cookie); err == nil {
			t.Fatalf("Callback accepted invalid callback %q", raw)
		}
	}
	if provider.exchanges() != 0 {
		t.Fatal("invalid callback reached token endpoint")
	}
}

type testProvider struct {
	server                    *httptest.Server
	exchangeN                 atomic.Int32
	key                       *rsa.PrivateKey
	mu                        sync.Mutex
	nonce                     string
	groups                    []string
	tokenStatus               int
	claims                    map[string]any
	corruptSignature          bool
	expectedVerifierChallenge string
}

func (p *testProvider) exchanges() int { return int(p.exchangeN.Load()) }

func (p *testProvider) setNonce(nonce string) {
	p.mu.Lock()
	p.nonce = nonce
	p.mu.Unlock()
}

func (p *testProvider) setGroups(groups []string) {
	p.mu.Lock()
	p.groups = append([]string(nil), groups...)
	p.mu.Unlock()
}

func (p *testProvider) setTokenStatus(status int) {
	p.mu.Lock()
	p.tokenStatus = status
	p.mu.Unlock()
}

func (p *testProvider) setClaims(claims map[string]any, corrupt bool) {
	p.mu.Lock()
	p.claims = claims
	p.corruptSignature = corrupt
	p.mu.Unlock()
}

func (p *testProvider) setExpectedVerifierChallenge(challenge string) {
	p.mu.Lock()
	p.expectedVerifierChallenge = challenge
	p.mu.Unlock()
}

func newTestProvider(t *testing.T) *testProvider {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	provider := &testProvider{key: key}
	provider.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			issuer := provider.server.URL
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"issuer":"` + issuer + `","authorization_endpoint":"` + issuer + `/authorize","token_endpoint":"` + issuer + `/token","jwks_uri":"` + issuer + `/jwks","id_token_signing_alg_values_supported":["RS256"]}`))
		case "/authorize":
			provider.mu.Lock()
			provider.nonce = r.URL.Query().Get("nonce")
			provider.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		case "/token":
			provider.exchangeN.Add(1)
			_ = r.ParseForm()
			provider.mu.Lock()
			status := provider.tokenStatus
			challenge := provider.expectedVerifierChallenge
			provider.mu.Unlock()
			if challenge != "" {
				sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
				if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
					http.Error(w, "bad verifier", http.StatusBadRequest)
					return
				}
			}
			if status != 0 {
				http.Error(w, "rejected", status)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			idToken, err := provider.idToken()
			if err != nil {
				http.Error(w, "token", http.StatusInternalServerError)
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"access","token_type":"Bearer","id_token":"` + idToken + `"}`))
		case "/jwks":
			n := base64.RawURLEncoding.EncodeToString(provider.key.PublicKey.N.Bytes())
			_, _ = w.Write([]byte(`{"keys":[{"kty":"RSA","kid":"test-key","use":"sig","alg":"RS256","n":"` + n + `","e":"AQAB"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *testProvider) idToken() (string, error) {
	p.mu.Lock()
	nonce := p.nonce
	groups := append([]string(nil), p.groups...)
	overrides := p.claims
	corrupt := p.corruptSignature
	p.mu.Unlock()
	if groups == nil {
		groups = []string{"invoice-admins"}
	}
	claims := map[string]any{
		"iss": p.server.URL, "aud": "invoice-generator", "sub": "person-1", "name": "Person",
		"email": "person@example.test", "nonce": nonce, "groups": groups,
		"iat": time.Now().Unix(), "auth_time": time.Now().Unix(), "exp": time.Now().Add(10 * time.Minute).Unix(),
	}
	for key, value := range overrides {
		claims[key] = value
	}
	encodedClaims, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","kid":"test-key","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString(encodedClaims)
	sum := crypto.SHA256.New()
	_, _ = sum.Write([]byte(header + "." + payload))
	signature, err := rsa.SignPKCS1v15(rand.Reader, p.key, crypto.SHA256, sum.Sum(nil))
	if err != nil {
		return "", err
	}
	if corrupt {
		signature[0] ^= 1
	}
	return header + "." + payload + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func newTestManager(t *testing.T, provider *testProvider, repository *store.Store) *Manager {
	t.Helper()
	issuer := provider.server.URL
	manager, err := NewManager(context.Background(), repository, Config{IssuerURL: issuer, ClientID: "invoice-generator", ClientSecret: "secret", RedirectURL: "https://app.example.test/auth/callback", Development: true, SessionKey: strings.Repeat("s", 32), TransactionKey: strings.Repeat("t", 32), HTTPClient: provider.server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func testStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}
