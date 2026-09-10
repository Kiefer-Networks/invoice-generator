package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOIDCDiscoveryRejectsAllRedirects(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var targetHits atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetHits.Add(1)
				w.WriteHeader(http.StatusNoContent)
			}))
			t.Cleanup(target.Close)
			redirector := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, target.URL+"/.well-known/openid-configuration", status)
			}))
			t.Cleanup(redirector.Close)
			_, err := NewManager(context.Background(), nil, Config{IssuerURL: redirector.URL, ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app.example.test/auth/callback", SessionKey: strings.Repeat("s", 32), TransactionKey: strings.Repeat("t", 32), Development: true, HTTPClient: redirector.Client()})
			if err == nil || targetHits.Load() != 0 {
				t.Fatal("NewManager followed an OIDC discovery redirect")
			}
		})
	}
}

func TestOIDCResponseLimiterPreservesCloseSetsTimeoutAndRejectsOverflow(t *testing.T) {
	t.Parallel()
	closed := &trackingBody{Reader: strings.NewReader("ok")}
	client := boundedClient(&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: closed, Header: make(http.Header)}, nil
	})})
	if client.Timeout <= 0 {
		t.Fatal("bounded OIDC client has no timeout")
	}
	response, err := client.Get("https://issuer.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if !closed.closed.Load() {
		t.Fatal("response limiter did not close the underlying response body")
	}

	overflow := boundedClient(&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maxOIDCDocumentBytes+2))), Header: make(http.Header)}, nil
	})})
	response, err = overflow.Get("https://issuer.example.test")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.ReadAll(response.Body); err == nil {
		t.Fatal("response limiter accepted an oversized OIDC response")
	}
}

func TestOIDCResponseLimiterRejectsOverflowReturnedWithEOF(t *testing.T) {
	t.Parallel()
	body := &limitedReadCloser{ReadCloser: io.NopCloser(&eofReader{remaining: maxOIDCDocumentBytes + 1}), reader: &io.LimitedReader{R: &eofReader{remaining: maxOIDCDocumentBytes + 1}, N: maxOIDCDocumentBytes + 1}}
	// A legal reader may return its final bytes and io.EOF together. Feed that
	// shape directly to the limiter so the overflow decision cannot depend on
	// err being nil.
	body.reader = &io.LimitedReader{R: &eofReader{remaining: maxOIDCDocumentBytes + 1}, N: maxOIDCDocumentBytes + 1}
	if _, err := body.Read(make([]byte, maxOIDCDocumentBytes+1)); err == nil {
		t.Fatal("limiter accepted max+1 bytes returned with io.EOF")
	}
}

func TestBeginPKCEChallengeMatchesEncryptedVerifier(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	manager := newTestManager(t, provider, nil)
	redirect, cookie, err := manager.Begin("/")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := manager.openTransaction(cookie)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	sum := sha256.Sum256([]byte(tx.Verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if u.Query().Get("code_challenge") != want {
		t.Fatal("PKCE challenge was not derived from stored verifier")
	}
}

func TestCallbackPersistsOnlyExactSessionAndCSRFHMACs(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	database := testStore(t)
	manager := newTestManager(t, provider, database)
	redirect, transactionCookie, err := manager.Begin("/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	provider.setNonce(u.Query().Get("nonce"))
	provider.setExpectedVerifierChallenge(u.Query().Get("code_challenge"))
	callback, _ := url.Parse("https://app.example.test/auth/callback?code=code&state=" + u.Query().Get("state"))
	result, err := manager.Callback(context.Background(), callback, transactionCookie)
	if err != nil {
		t.Fatal(err)
	}
	token, csrf, ok := splitSessionCookie(result.SessionCookie)
	if !ok {
		t.Fatal("callback did not return a bound session cookie")
	}
	var tokenHash, csrfHash []byte
	if err := database.DB().QueryRowContext(context.Background(), "SELECT token_hash, csrf_secret_hash FROM sessions").Scan(&tokenHash, &csrfHash); err != nil {
		t.Fatal(err)
	}
	if string(tokenHash) != string(manager.keyedHash(token)) || string(csrfHash) != string(manager.keyedHash(csrf)) {
		t.Fatal("SQLite does not contain the exact keyed session and CSRF hashes")
	}
	for _, raw := range []string{token, csrf} {
		var count int
		if err := database.DB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM sessions WHERE token_hash=? OR csrf_secret_hash=?", []byte(raw), []byte(raw)).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatal("SQLite contains a raw session or CSRF value")
		}
	}
	if err := manager.ValidateCSRF(context.Background(), result.SessionCookie, result.CSRFToken); err != nil {
		t.Fatal(err)
	}
}

func TestAuthorizationTransactionValuesAreIndependentAndExpire(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	provider := newTestProvider(t)
	manager := newTestManager(t, provider, testStore(t))
	manager.now = func() time.Time { return now }
	redirect, cookie, err := manager.Begin("/")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := manager.openTransaction(cookie)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{tx.State, tx.Nonce, tx.Verifier} {
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(decoded) != 32 {
			t.Fatal("transaction value is not an independent 32-byte random value")
		}
	}
	if tx.State == tx.Nonce || tx.State == tx.Verifier || tx.Nonce == tx.Verifier {
		t.Fatal("transaction state, nonce, and verifier were reused")
	}
	now = now.Add(transactionLifetime)
	u, _ := url.Parse(redirect)
	callback, _ := url.Parse("https://app.example.test/auth/callback?code=x&state=" + u.Query().Get("state"))
	result, err := manager.Callback(context.Background(), callback, cookie)
	if err == nil || result.TransactionCookie == nil || result.TransactionCookie.MaxAge >= 0 {
		t.Fatal("expired transaction was accepted or not deleted")
	}
}

func TestCallbackRejectsAdversarialIDTokenClaims(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		claims  map[string]any
		corrupt bool
	}{
		{"bad signature", nil, true}, {"issuer", map[string]any{"iss": "https://other.example.test"}, false}, {"audience", map[string]any{"aud": "other"}, false}, {"authorized party", map[string]any{"azp": "other"}, false}, {"nonce", map[string]any{"nonce": "other"}, false}, {"expired", map[string]any{"exp": time.Now().Add(-time.Minute).Unix()}, false}, {"issued at", map[string]any{"iat": time.Now().Add(3 * time.Minute).Unix()}, false}, {"authentication time", map[string]any{"auth_time": time.Now().Add(-16 * time.Minute).Unix()}, false}, {"empty subject", map[string]any{"sub": ""}, false}, {"missing group", map[string]any{"groups": []string{}}, false}, {"wrong group", map[string]any{"groups": []string{"billing"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := newTestProvider(t)
			provider.setClaims(tc.claims, tc.corrupt)
			manager := newTestManager(t, provider, testStore(t))
			redirect, cookie, err := manager.Begin("/")
			if err != nil {
				t.Fatal(err)
			}
			u, _ := url.Parse(redirect)
			provider.setNonce(u.Query().Get("nonce"))
			callback, _ := url.Parse("https://app.example.test/auth/callback?code=code&state=" + u.Query().Get("state"))
			result, err := manager.Callback(context.Background(), callback, cookie)
			if err == nil {
				t.Fatal("callback accepted adversarial ID token")
			}
			if result.TransactionCookie == nil || result.TransactionCookie.MaxAge >= 0 {
				t.Fatal("failed callback did not delete transaction cookie")
			}
		})
	}
}

func TestCallbackRejectsMalformedQueriesAndErrorCodeMix(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	manager := newTestManager(t, provider, testStore(t))
	redirect, cookie, err := manager.Begin("/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	for _, raw := range []string{"https://wrong.example.test/auth/callback?code=x&state=" + u.Query().Get("state"), "https://app.example.test/auth/callback?error=denied&code=x&state=" + u.Query().Get("state"), "https://app.example.test/auth/callback?code=%zz&state=x"} {
		callback, _ := url.Parse(raw)
		result, err := manager.Callback(context.Background(), callback, cookie)
		if err == nil || result.TransactionCookie == nil || result.TransactionCookie.MaxAge >= 0 {
			t.Fatalf("Callback accepted malformed callback %q", raw)
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type trackingBody struct {
	io.Reader
	closed atomic.Bool
}

type eofReader struct{ remaining int64 }

func (r *eofReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, io.EOF
	}
	n := len(p)
	if int64(n) > r.remaining {
		n = int(r.remaining)
	}
	r.remaining -= int64(n)
	return n, io.EOF
}

func (b *trackingBody) Close() error { b.closed.Store(true); return nil }

func TestBeginRequestsRecentAuthenticationAndBoundsTransactions(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	provider := newTestProvider(t)
	manager := newTestManager(t, provider, nil)
	manager.now = func() time.Time { return now }
	redirect, _, err := manager.Begin("/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	if u.Query().Get("max_age") != "900" {
		t.Fatalf("max_age=%q, want 900", u.Query().Get("max_age"))
	}
	now = now.Add(transactionLifetime + time.Second)
	if _, _, err := manager.Begin("/"); err != nil {
		t.Fatal(err)
	}
	if len(manager.transactions) != 1 {
		t.Fatalf("expired transactions were retained: %d", len(manager.transactions))
	}
	for i := 1; i < 1024; i++ {
		if _, _, err := manager.Begin("/"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := manager.Begin("/"); err == nil {
		t.Fatal("Begin accepted more than the transaction capacity")
	}
}

func TestManagerRejectsIdenticalSessionAndTransactionKeys(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	_, err := NewManager(context.Background(), nil, Config{IssuerURL: provider.server.URL, ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app.example.test/auth/callback", SessionKey: strings.Repeat("x", 32), TransactionKey: strings.Repeat("x", 32), Development: true, HTTPClient: provider.server.Client()})
	if err == nil {
		t.Fatal("NewManager accepted identical session and transaction keys")
	}
}

func TestTokenExchangeFailureMakesOneRequest(t *testing.T) {
	t.Parallel()
	provider := newTestProvider(t)
	provider.setTokenStatus(http.StatusUnauthorized)
	manager := newTestManager(t, provider, testStore(t))
	redirect, cookie, err := manager.Begin("/")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(redirect)
	callback, _ := url.Parse("https://app.example.test/auth/callback?code=code-1&state=" + u.Query().Get("state"))
	result, err := manager.Callback(context.Background(), callback, cookie)
	if err == nil {
		t.Fatal("callback accepted a token endpoint failure")
	}
	if result.TransactionCookie == nil || result.TransactionCookie.MaxAge >= 0 {
		t.Fatal("failed callback did not delete transaction cookie")
	}
	if provider.exchanges() != 1 {
		t.Fatalf("token requests=%d, want 1", provider.exchanges())
	}
}
