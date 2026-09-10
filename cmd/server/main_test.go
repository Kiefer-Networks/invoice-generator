package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestDevExplicitAndIsolated(t *testing.T) {
	root := filepath.Join(t.TempDir(), "dev")
	cfg, e := ParseConfig([]string{"-dev", "-dev-root", root}, func(string) string { return "" })
	if e != nil {
		t.Fatal(e)
	}
	if !cfg.Development || cfg.Database != filepath.Join(root, "development.sqlite") {
		t.Fatalf("isolated config: %+v", cfg)
	}
	for _, args := range [][]string{{"-listen", "0.0.0.0:8080"}, {"-database", filepath.Join(t.TempDir(), "prod.sqlite")}, {"-session-key-file", "production-secret"}, {"-pocket-id-issuer", "https://id.example.com"}, {"-paperless-url", "https://paperless.example.com"}} {
		if _, e = ParseConfig(append([]string{"-dev", "-dev-root", root}, args...), func(string) string { return "" }); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if _, e = ParseConfig([]string{"-dev", "-dev-root", root}, func(k string) string {
		if k == "INVOICE_DATABASE" {
			return "prod.sqlite"
		}
		return ""
	}); e == nil {
		t.Fatal("inherited production database")
	}
	if _, e = ParseConfig(nil, func(k string) string {
		if k == "INVOICE_DEVELOPMENT" {
			return "1"
		}
		return ""
	}); e == nil {
		t.Fatal("development enabled by environment")
	}
}

func TestRecoveryCommandsRoundtripAndIntegrity(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	database := filepath.Join(root, "live.sqlite")
	docs := filepath.Join(root, "documents")
	if err := os.Mkdir(docs, 0700); err != nil {
		t.Error(err)
	}
	s, e := store.Open(ctx, database)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	_ = s.Close()
	key := filepath.Join(root, "key")
	if err := os.WriteFile(key, bytes.Repeat([]byte{9}, 32), 0600); err != nil {
		t.Error(err)
	}
	archive := filepath.Join(root, "backup.enc")
	target := filepath.Join(root, "recovered")
	commands := [][]string{
		{"integrity-check", "-database", database, "-document-root", docs},
		{"backup", "-database", database, "-document-root", docs, "-output", archive, "-key-file", key},
		{"backup", "verify", "-archive", archive, "-key-file", key},
		{"restore", "-archive", archive, "-key-file", key, "-target-root", target, "-confirm"},
		{"integrity-check", "-database", filepath.Join(target, "database.sqlite"), "-document-root", filepath.Join(target, "documents")},
	}
	for _, args := range commands {
		var out bytes.Buffer
		if e = runRecoveryCommand(ctx, args, &out); e != nil {
			t.Fatalf("%s: %v", args[0], e)
		}
		if !strings.Contains(out.String(), "success") || strings.Contains(out.String(), root) {
			t.Fatal("unsafe or missing audit outcome", out.String())
		}
	}
}

func TestRecoveryCommandsRequireExplicitPathsAndConfirmation(t *testing.T) {
	for _, args := range [][]string{
		{"backup"}, {"backup", "-database", "relative"}, {"backup", "-passphrase", "never-print-this"},
		{"restore", "-archive", "relative", "-target-root", "relative"}, {"integrity-check", "positional-secret"},
		{"restore", "-archive", filepath.Join(t.TempDir(), "archive"), "-target-root", filepath.Join(t.TempDir(), "target"), "-key-file", filepath.Join(t.TempDir(), "key")},
	} {
		var out bytes.Buffer
		if e := runRecoveryCommand(context.Background(), args, &out); e == nil {
			t.Fatal("unsafe options accepted")
		}
		if strings.Contains(out.String(), "never-print-this") || strings.Contains(out.String(), "positional-secret") {
			t.Fatal("secret in output")
		}
	}
}

func TestServeRefusesExistingServiceLockBeforeDatabaseWrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "db.sqlite")
	release, e := store.AcquireServiceLock(path)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	if e = serve(Config{Database: path}); e == nil {
		t.Fatal("second writer allowed")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("database opened before service lock")
	}
}

func TestServeRejectsHardlinkAliasBeforeSQLite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "source.sqlite")
	db, release, e := store.OpenService(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	defer release()
	defer db.Close()
	if e = db.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	alias := filepath.Join(root, "alias.sqlite")
	if e = os.Link(path, alias); e != nil {
		t.Fatal(e)
	}
	if e = serve(Config{Database: alias}); e == nil {
		t.Fatal("alias serve accepted")
	}
	for _, suffix := range []string{"-wal", "-shm", ".service-lock"} {
		if _, e = os.Lstat(alias + suffix); !os.IsNotExist(e) {
			t.Fatal("alias SQLite sidecar created", suffix)
		}
	}
}

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

func TestRevokeSpecificSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().Exec(`INSERT INTO oidc_users (id,issuer,subject,display_name,active) VALUES ('u','https://issuer.test','subject','User',1)`); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"11111111111111111111111111111111", "22222222222222222222222222222222"} {
		if _, err := s.DB().Exec(`INSERT INTO sessions (id,user_id,token_hash,csrf_secret_hash,authorization_expires_at,expires_at) VALUES (?,'u',?,?, '9999999999999999999','9999999999999999999')`, id, []byte(id+"token"), []byte(id+"csrf")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runSessionCommand(context.Background(), []string{"revoke", "-database", path, "-id", "11111111111111111111111111111111"}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	check, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var count int
	if err := check.DB().QueryRow(`SELECT count(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("sessions remaining=%d, want 1", count)
	}
}

func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		DocumentRoot: privateDocumentRoot(t),
		Listen:       "127.0.0.1:8443", AllowedHosts: []string{"app.example.test"},
		TLSCertFile: "cert.pem", TLSKeyFile: "key.pem", Database: filepath.Join(t.TempDir(), "app.db"),
		PocketIDIssuer: "https://id.example.test", PocketIDClientID: "invoice-generator",
		ClientSecretFile: writeSecret(t, "client-secret"), SessionKeyFile: writeSecret(t, encodedKey("0123456789abcdefghijklmnopqrstuv")),
		TransactionKeyFile: writeSecret(t, encodedKey("zyxwvutsrqponmlkjihgfedcba987654")), CallbackURL: "https://app.example.test/auth/callback",
		RequiredGroup: "invoice-admins", BodyLimit: 1 << 20,
	}
}

func privateDocumentRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	// Match production provisioning explicitly; do not rely on the test
	// runner's temporary-directory mode or the process umask.
	if err := os.Chmod(root, 0700); err != nil { // #nosec G302 -- Owner-only directory traversal requires 0700.
		t.Fatal(err)
	}
	return root
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

func TestConfigRejectsWeakApplicationKeys(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Config)
	}{
		{"repeated session", func(c *Config) { c.SessionKeyFile = writeSecret(t, encodedKey(strings.Repeat("s", 32))) }},
		{"repeated csrf", func(c *Config) { c.TransactionKeyFile = writeSecret(t, encodedKey(strings.Repeat("t", 32))) }},
		{"default session", func(c *Config) { c.SessionKeyFile = writeSecret(t, encodedKey(strings.Repeat("\x00", 32))) }},
		{"default csrf", func(c *Config) { c.TransactionKeyFile = writeSecret(t, encodedKey("0123456789abcdef0123456789abcdef")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			tc.mutate(&cfg)
			if err := cfg.Validate(); err == nil {
				t.Fatal("accepted weak application key")
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
