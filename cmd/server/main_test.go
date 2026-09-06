package main

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

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
		ClientSecretFile: writeSecret(t, "client-secret"), SessionKeyFile: writeSecret(t, "0123456789abcdefghijklmnopqrstuv"),
		TransactionKeyFile: writeSecret(t, "zyxwvutsrqponmlkjihgfedcba987654"), CallbackURL: "https://app.example.test/auth/callback",
		RequiredGroup: "invoice-admins", BodyLimit: 1 << 20,
	}
}

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
		{"short session key", func(c *Config) { c.SessionKeyFile = writeSecret(t, "short") }},
		{"default session key", func(c *Config) { c.SessionKeyFile = writeSecret(t, strings.Repeat("x", 32)) }},
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

func TestConfigRejectsWorldReadableSecretOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no POSIX secret mode bits")
	}
	cfg := testConfig(t)
	cfg.SessionKeyFile = writeSecretMode(t, strings.Repeat("s", 32), 0644)
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate accepted a world-readable secret")
	}
}
