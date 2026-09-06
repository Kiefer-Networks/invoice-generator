package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOIDCDiscoveryRejectsHTTPIssuerOutsideDevelopment(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()

	_, err := NewManager(context.Background(), nil, Config{
		IssuerURL:    server.URL,
		ClientID:     "invoice-generator",
		ClientSecret: "secret",
		RedirectURL:  "https://app.example.test/auth/callback",
	})
	if err == nil {
		t.Fatal("NewManager accepted an HTTP issuer outside development")
	}
}

func TestNewManagerRequiresExactCallbackPath(t *testing.T) {
	provider := newTestProvider(t)
	_, err := NewManager(context.Background(), nil, Config{IssuerURL: provider.server.URL, ClientID: "invoice-generator", ClientSecret: "secret", RedirectURL: "https://app.example.test/not-callback", Development: true, SessionKey: strings.Repeat("s", 32), TransactionKey: strings.Repeat("t", 32), HTTPClient: provider.server.Client()})
	if err == nil {
		t.Fatal("NewManager accepted a callback outside /auth/callback")
	}
}

func TestOIDCDiscoveryRejectsMismatchedAndUnsafeMetadata(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body func(string) string
	}{
		{"issuer mismatch", func(string) string {
			return `{"issuer":"https://other.example.test","authorization_endpoint":"https://other.example.test/a","token_endpoint":"https://other.example.test/t","jwks_uri":"https://other.example.test/j","id_token_signing_alg_values_supported":["RS256"]}`
		}},
		{"missing endpoint", func(issuer string) string {
			return `{"issuer":"` + issuer + `","authorization_endpoint":"","token_endpoint":"` + issuer + `/t","jwks_uri":"` + issuer + `/j","id_token_signing_alg_values_supported":["RS256"]}`
		}},
		{"cross origin endpoint", func(issuer string) string {
			return `{"issuer":"` + issuer + `","authorization_endpoint":"https://attacker.example.test/a","token_endpoint":"` + issuer + `/t","jwks_uri":"` + issuer + `/j","id_token_signing_alg_values_supported":["RS256"]}`
		}},
		{"symmetric algorithm", func(issuer string) string {
			return `{"issuer":"` + issuer + `","authorization_endpoint":"` + issuer + `/a","token_endpoint":"` + issuer + `/t","jwks_uri":"` + issuer + `/j","id_token_signing_alg_values_supported":["HS256"]}`
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := discoveryServer(t, func(issuer string) string { return tc.body(issuer) }, nil)
			_, err := NewManager(context.Background(), nil, Config{IssuerURL: server.URL, ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app.example.test/auth/callback", SessionKey: strings.Repeat("s", 32), TransactionKey: strings.Repeat("t", 32), Development: true, HTTPClient: server.Client()})
			if err == nil {
				t.Fatal("NewManager accepted unsafe Pocket ID metadata")
			}
		})
	}
}

func TestOIDCDiscoveryRejectsOversizedAndStaleDocuments(t *testing.T) {
	t.Parallel()
	oversized := discoveryServer(t, func(string) string { return strings.Repeat("x", maxOIDCDocumentBytes+1) }, nil)
	_, err := NewManager(context.Background(), nil, Config{IssuerURL: oversized.URL, ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app.example.test/auth/callback", SessionKey: strings.Repeat("s", 32), TransactionKey: strings.Repeat("t", 32), Development: true, HTTPClient: oversized.Client()})
	if err == nil {
		t.Fatal("NewManager accepted an oversized discovery document")
	}

	stale := discoveryServer(t, func(issuer string) string { return validDiscovery(issuer) }, func(w http.ResponseWriter) {
		w.Header().Set("Date", time.Now().Add(-2*time.Hour).UTC().Format(http.TimeFormat))
		w.Header().Set("Cache-Control", "max-age=60")
	})
	_, err = NewManager(context.Background(), nil, Config{IssuerURL: stale.URL, ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app.example.test/auth/callback", SessionKey: strings.Repeat("s", 32), TransactionKey: strings.Repeat("t", 32), Development: true, HTTPClient: stale.Client()})
	if err == nil {
		t.Fatal("NewManager accepted a stale discovery document")
	}
}

func discoveryServer(t *testing.T, body func(string) string, headers func(http.ResponseWriter)) *httptest.Server {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		if headers != nil {
			headers(w)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body(server.URL)))
	}))
	t.Cleanup(server.Close)
	return server
}

func validDiscovery(issuer string) string {
	return `{"issuer":"` + issuer + `","authorization_endpoint":"` + issuer + `/a","token_endpoint":"` + issuer + `/t","jwks_uri":"` + issuer + `/j","id_token_signing_alg_values_supported":["RS256"]}`
}
