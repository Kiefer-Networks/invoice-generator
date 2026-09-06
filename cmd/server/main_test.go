package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestProductionManagerUsesProtectedFileConfiguration(t *testing.T) {
	var issuer string
	provider := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `{"issuer":%q,"authorization_endpoint":%q,"token_endpoint":%q,"jwks_uri":%q,"id_token_signing_alg_values_supported":["RS256"]}`, issuer, issuer+"/authorize", issuer+"/token", issuer+"/jwks")
	}))
	defer provider.Close()
	issuer = provider.URL
	cfg := testConfig(t)
	cfg.PocketIDIssuer = issuer
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), cfg.Database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := newAuthManager(context.Background(), db, cfg, provider.Client()); err != nil {
		t.Fatal(err)
	}
}

func TestRevokeAllSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO oidc_users (id,issuer,subject,display_name,active) VALUES ('u','https://issuer.test','subject','User',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO sessions (id,user_id,token_hash,csrf_secret_hash,authorization_expires_at,expires_at) VALUES ('s','u',x'01',x'02','9999999999999999999','9999999999999999999')`); err != nil {
		t.Fatal(err)
	}
	if err := revokeAllSessions(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB().QueryRow("SELECT count(*) FROM sessions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("sessions remaining = %d", count)
	}
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		Listen: "127.0.0.1:8443", AllowedHosts: []string{"app.example.test"},
		TLSCertFile: "cert.pem", TLSKeyFile: "key.pem", Database: filepath.Join(t.TempDir(), "app.db"),
		PocketIDIssuer: "https://id.example.test", PocketIDClientID: "invoice-generator",
		ClientSecretFile: writeSecret(t, "client-secret"), SessionKeyFile: writeSecret(t, encodedKey("0123456789abcdefghijklmnopqrstuv")),
		TransactionKeyFile: writeSecret(t, encodedKey("zyxwvutsrqponmlkjihgfedcba987654")), CallbackURL: "https://app.example.test/auth/callback",
		RequiredGroup: "invoice-admins", BodyLimit: 1 << 20,
	}
}

func encodedKey(value string) string { return base64.RawStdEncoding.EncodeToString([]byte(value)) }

func writeSecret(t *testing.T, value string) string { return writeSecretMode(t, value, 0600) }
func writeSecretMode(t *testing.T, value string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(value), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestConfigRejectsUnsafeSettings(t *testing.T) {
	t.Parallel()
	valid := testConfig(t)
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"missing client secret", func(c *Config) { c.ClientSecretFile = "" }},
		{"invalid callback path", func(c *Config) { c.CallbackURL = "https://app.example.test/not-callback" }},
		{"http issuer", func(c *Config) { c.PocketIDIssuer = "http://id.example.test" }},
		{"invalid callback", func(c *Config) { c.CallbackURL = "https://other.example.test/callback#fragment" }},
		{"callback host not allowed", func(c *Config) { c.CallbackURL = "https://other.example.test/auth/callback" }},
		{"missing group", func(c *Config) { c.RequiredGroup = "" }},
		{"wrong group", func(c *Config) { c.RequiredGroup = "administrators" }},
		{"wildcard host", func(c *Config) { c.AllowedHosts = []string{"*"} }},
		{"malformed proxy", func(c *Config) { c.TrustedProxies = []netip.Prefix{{}} }},
		{"no tls or proxy", func(c *Config) { c.TLSCertFile, c.TLSKeyFile = "", "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate accepted unsafe configuration")
			}
		})
	}
}

func TestConfigRejectsUnsafeDevelopmentSettings(t *testing.T) {
	t.Parallel()
	valid := testConfig(t)
	valid.Development = true
	valid.Listen = "127.0.0.1:8080"
	valid.AllowedHosts = []string{"127.0.0.1"}
	valid.PocketIDIssuer = "http://127.0.0.1:9000"
	valid.CallbackURL = "http://127.0.0.1:8080/auth/callback"
	valid.TLSCertFile, valid.TLSKeyFile = "", ""
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"public listener", func(c *Config) { c.Listen = "0.0.0.0:8080" }},
		{"real pocket id", func(c *Config) { c.PocketIDIssuer = "https://id.example.test" }},
		{"paperless", func(c *Config) { c.PaperlessURL = "http://127.0.0.1:8000" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("Validate accepted unsafe development configuration")
			}
		})
	}
}

func TestConfigRejectsBroadSecretModesOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX secret mode bits")
	}
	for _, mode := range []os.FileMode{0640, 0660} {
		cfg := testConfig(t)
		cfg.SessionKeyFile = writeSecretMode(t, encodedKey("0123456789abcdefghijklmnopqrstuv"), mode)
		if err := cfg.Validate(); err == nil {
			t.Fatalf("Validate accepted mode %04o", mode)
		}
	}
}
