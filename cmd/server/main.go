// Command server runs the authenticated invoice administration application.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/auth"
	"github.com/kiefer-networks/invoice-generator/internal/paperless"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"github.com/kiefer-networks/invoice-generator/internal/web"
)

const defaultBodyLimit int64 = 1 << 20

// Config contains the deployment boundary for the HTTP service. Secrets are
// represented only by file paths so they cannot accidentally reach logs.
type Config struct {
	DevAssetsDir                                         string
	DevRoot, DevPaperlessState                           string
	devPaperless                                         func() (*paperless.Client, error)
	DocumentRoot                                         string
	Listen, Database                                     string
	AllowedHosts                                         []string
	TrustedProxies                                       []netip.Prefix
	TLSCertFile, TLSKeyFile                              string
	Development                                          bool
	BodyLimit                                            int64
	PocketIDIssuer, PocketIDClientID, CallbackURL        string
	ClientSecretFile, SessionKeyFile, TransactionKeyFile string
	PaperlessTokenFile                                   string
	RequiredGroup, PaperlessURL                          string
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "backup" || os.Args[1] == "restore" || os.Args[1] == "integrity-check") {
		ctx, stop := signal.NotifyContext(context.Background(), terminationSignals()...)
		err := runRecoveryCommand(ctx, os.Args[1:], os.Stdout)
		stop()
		if err != nil {
			fmt.Fprintln(os.Stderr, "recovery: failure")
			os.Exit(1)
		}
		return
	}
	if len(os.Args) >= 3 && os.Args[1] == "sessions" {
		if err := runSessionCommand(context.Background(), os.Args[2:], os.Getenv); err != nil {
			fmt.Fprintln(os.Stderr, "server:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: server {serve|backup|restore|integrity-check|sessions revoke-all} [flags]")
		os.Exit(2)
	}
	cfg, err := ParseConfig(os.Args[2:], os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "server configuration:", err)
		os.Exit(2)
	}
	if err := serve(cfg); err != nil {
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

func runSessionCommand(ctx context.Context, args []string, getenv func(string) string) error {
	if len(args) == 0 {
		return errors.New("usage: server sessions {revoke|revoke-all} -database PATH")
	}
	command := args[0]
	fs := flag.NewFlagSet("sessions "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	database := fs.String("database", strings.TrimSpace(getenv("INVOICE_DATABASE")), "SQLite database")
	id := fs.String("id", "", "opaque session identifier")
	if err := fs.Parse(args[1:]); err != nil || fs.NArg() != 0 || *database == "" {
		return errors.New("invalid session command")
	}
	switch command {
	case "revoke-all":
		if *id != "" {
			return errors.New("session id is not accepted for revoke-all")
		}
		return revokeAllSessions(ctx, *database)
	case "revoke":
		decoded, err := base64.RawURLEncoding.DecodeString(*id)
		if err != nil || len(decoded) != 16 || base64.RawURLEncoding.EncodeToString(decoded) != *id {
			return errors.New("session id is invalid")
		}
		return revokeSessionByID(ctx, *database, *id)
	default:
		return errors.New("unknown session command")
	}
}

func terminationSignals() []os.Signal {
	// Go maps Windows console-close/logoff/shutdown notifications to SIGTERM.
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}

// Recovery accepts explicit absolute paths only. Keys are raw 32-byte protected
// files; key contents/passphrases are never accepted in flags or environment.
// Restore requires -confirm and a new target root; operators stop normal service
// before pointing its configuration at that root. Existing data is never moved.
func runRecoveryCommand(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return store.ErrBackup
	}
	action := args[0]
	args = args[1:]
	if action == "backup" && len(args) > 0 && args[0] == "verify" {
		action = "verify"
		args = args[1:]
	}
	fs := flag.NewFlagSet("recovery", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var database, docs, archive, output, key, target string
	var confirm bool
	switch action {
	case "backup":
		fs.StringVar(&database, "database", "", "absolute SQLite path")
		fs.StringVar(&docs, "document-root", "", "absolute document root")
		fs.StringVar(&output, "output", "", "new absolute archive path")
	case "verify":
		fs.StringVar(&archive, "archive", "", "absolute archive path")
	case "restore":
		fs.StringVar(&archive, "archive", "", "absolute archive path")
		fs.StringVar(&target, "target-root", "", "new absolute recovery root")
		fs.BoolVar(&confirm, "confirm", false, "confirm activation into the new root")
	case "integrity-check":
		fs.StringVar(&database, "database", "", "absolute SQLite path")
		fs.StringVar(&docs, "document-root", "", "absolute document root")
	default:
		return store.ErrBackup
	}
	if action != "integrity-check" {
		fs.StringVar(&key, "key-file", "", "absolute protected raw 32-byte key file")
	}
	if e := fs.Parse(args); e != nil || fs.NArg() != 0 {
		return store.ErrBackup
	}
	paths := []string{key}
	if action == "integrity-check" {
		paths = nil
	}
	switch action {
	case "backup":
		paths = append(paths, database, docs, output)
	case "integrity-check":
		paths = append(paths, database, docs)
	case "verify":
		paths = append(paths, archive)
	case "restore":
		if !confirm {
			return store.ErrBackup
		}
		paths = append(paths, archive, target)
	}
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			return store.ErrBackup
		}
	}
	var e error
	switch action {
	case "backup":
		_, e = store.Backup(ctx, store.BackupOptions{Database: database, DocumentRoot: docs, Output: output, KeyFile: key})
	case "verify":
		e = store.VerifyBackup(ctx, store.VerifyOptions{Archive: archive, KeyFile: key})
	case "restore":
		e = store.Restore(ctx, store.RestoreOptions{Archive: archive, KeyFile: key, TargetRoot: target, Confirm: confirm})
	case "integrity-check":
		e = store.IntegrityCheck(ctx, database, docs)
	}
	if e != nil {
		return store.ErrBackup
	}
	_, e = fmt.Fprintln(out, action+": success")
	return e
}

// ParseConfig reads non-secret configuration from flags and environment.
func ParseConfig(args []string, getenv func(string) string) (Config, error) {
	for _, arg := range args {
		if arg == "-dev" || arg == "--dev" || arg == "-dev=true" || arg == "--dev=true" {
			return parseDevelopment(args, getenv)
		}
	}
	if strings.TrimSpace(getenv("INVOICE_DEVELOPMENT")) != "" {
		return Config{}, errors.New("development requires explicit -dev; environment activation is forbidden")
	}
	value := func(name, fallback string) string {
		if v := strings.TrimSpace(getenv(name)); v != "" {
			return v
		}
		return fallback
	}
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	listen := fs.String("listen", value("INVOICE_LISTEN", ""), "listener address")
	database := fs.String("database", value("INVOICE_DATABASE", ""), "SQLite database")
	documentRoot := fs.String("document-root", value("INVOICE_DOCUMENT_ROOT", ""), "absolute, durably pre-provisioned protected document storage root")
	hosts := fs.String("allowed-hosts", value("INVOICE_ALLOWED_HOSTS", ""), "comma-separated hosts")
	proxies := fs.String("trusted-proxies", value("INVOICE_TRUSTED_PROXIES", ""), "comma-separated proxy CIDRs")
	cert := fs.String("tls-cert", value("INVOICE_TLS_CERT", ""), "TLS certificate file")
	key := fs.String("tls-key", value("INVOICE_TLS_KEY", ""), "TLS key file")
	dev := fs.Bool("dev", false, "development mode")
	issuer := fs.String("pocket-id-issuer", value("INVOICE_POCKET_ID_ISSUER", ""), "Pocket ID issuer")
	clientID := fs.String("pocket-id-client-id", value("INVOICE_POCKET_ID_CLIENT_ID", ""), "Pocket ID client ID")
	clientSecret := fs.String("pocket-id-client-secret-file", value("INVOICE_POCKET_ID_CLIENT_SECRET_FILE", ""), "Pocket ID client secret file")
	sessionKey := fs.String("session-key-file", value("INVOICE_SESSION_KEY_FILE", ""), "session key file")
	transactionKey := fs.String("transaction-key-file", value("INVOICE_TRANSACTION_KEY_FILE", ""), "transaction key file")
	callback := fs.String("callback-url", value("INVOICE_CALLBACK_URL", ""), "fixed Pocket ID callback URL")
	group := fs.String("required-group", value("INVOICE_REQUIRED_GROUP", "invoice-admins"), "required Pocket ID group")
	paperless := fs.String("paperless-url", value("INVOICE_PAPERLESS_URL", ""), "Paperless URL")
	paperlessToken := fs.String("paperless-token-file", value("INVOICE_PAPERLESS_TOKEN_FILE", ""), "protected mounted Paperless API token file")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	trusted, err := parsePrefixes(*proxies)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{DocumentRoot: *documentRoot, Listen: *listen, Database: *database, AllowedHosts: splitCSV(*hosts), TrustedProxies: trusted, TLSCertFile: *cert, TLSKeyFile: *key, Development: *dev, BodyLimit: defaultBodyLimit, PocketIDIssuer: *issuer, PocketIDClientID: *clientID, ClientSecretFile: *clientSecret, SessionKeyFile: *sessionKey, TransactionKeyFile: *transactionKey, CallbackURL: *callback, RequiredGroup: *group, PaperlessURL: *paperless, PaperlessTokenFile: *paperlessToken}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if c.Development {
		return validateDevelopment(c)
	}
	if c.Development && c.PaperlessTokenFile != "" {
		return errors.New("development cannot use a Paperless token file")
	}
	if c.PaperlessURL != "" {
		if e := paperless.ValidateURL(c.PaperlessURL, false); e != nil {
			return e
		}
		if c.PaperlessTokenFile != "" {
			if _, e := paperless.ReadToken(c.PaperlessTokenFile); e != nil && e.Error() != "configuration_missing" {
				return e
			}
		}
	}
	if !c.Development || c.DocumentRoot != "" {
		if e := validateDocumentRoot(c.DocumentRoot); e != nil {
			return e
		}
	}
	if c.Listen == "" || c.Database == "" || c.PocketIDClientID == "" {
		return errors.New("listener, database, and Pocket ID client ID are required")
	}
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil || host == "" {
		return errors.New("listener must be host:port")
	}
	if c.BodyLimit != defaultBodyLimit {
		return errors.New("body limit must be 1 MiB")
	}
	if len(c.AllowedHosts) == 0 {
		return errors.New("at least one allowed host is required")
	}
	for _, allowed := range c.AllowedHosts {
		if allowed == "" || allowed == "*" || strings.ContainsAny(allowed, "/\\ ") {
			return errors.New("allowed hosts must be explicit host names")
		}
	}
	for _, p := range c.TrustedProxies {
		if !p.IsValid() {
			return errors.New("trusted proxy network is invalid")
		}
	}
	if c.RequiredGroup != "invoice-admins" {
		return errors.New("required Pocket ID group must be invoice-admins")
	}
	issuer, err := parseHTTPURL(c.PocketIDIssuer)
	if err != nil {
		return fmt.Errorf("issuer for Pocket ID: %w", err)
	}
	callback, err := parseHTTPURL(c.CallbackURL)
	if err != nil {
		return fmt.Errorf("callback URL: %w", err)
	}
	if !allowedHost(callback.Hostname(), c.AllowedHosts) {
		return errors.New("callback URL host is not in the allowed host list")
	}
	if c.Development {
		if !loopbackHost(host) {
			return errors.New("development listener must be loopback")
		}
		if issuer.Scheme != "http" || !loopbackHost(issuer.Hostname()) {
			return errors.New("development requires the local Pocket ID fixture")
		}
		if callback.Scheme != "http" || !loopbackHost(callback.Hostname()) {
			return errors.New("development callback must be loopback HTTP")
		}
		if c.PaperlessURL != "" {
			return errors.New("development cannot use Paperless")
		}
	} else {
		if issuer.Scheme != "https" || callback.Scheme != "https" {
			return errors.New("production Pocket ID issuer and callback must use HTTPS")
		}
		if (c.TLSCertFile == "") != (c.TLSKeyFile == "") {
			return errors.New("TLS certificate and key must be configured together")
		}
		if c.TLSCertFile == "" && len(c.TrustedProxies) == 0 {
			return errors.New("production needs TLS or an explicitly trusted TLS proxy")
		}
	}
	if callback.Path != "/auth/callback" || callback.RawQuery != "" {
		return errors.New("callback URL must be the exact /auth/callback endpoint")
	}
	for _, secret := range []struct{ path, label string }{{c.ClientSecretFile, "Pocket ID client secret"}, {c.SessionKeyFile, "session key"}, {c.TransactionKeyFile, "transaction key"}} {
		if err := protectedSecretFile(secret.path, secret.label); err != nil {
			return err
		}
	}
	for _, key := range []struct{ path, label string }{{c.SessionKeyFile, "session key"}, {c.TransactionKeyFile, "CSRF key"}} {
		if err := validateApplicationKey(key.path, key.label); err != nil {
			return err
		}
	}
	return nil
}

func protectedSecretFile(path, label string) error {
	if path == "" {
		return fmt.Errorf("%s file is required", label)
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("invalid %s file", label)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s file permissions are too broad", label)
	}
	return nil
}
func validateApplicationKey(path, label string) error {
	data, err := os.ReadFile(path) // #nosec G304 -- Startup-only administrator-configured secret path; validateSecretFile checks its type and permissions.
	if err != nil {
		return fmt.Errorf("read %s: %w", label, err)
	}
	key, err := base64.RawStdEncoding.DecodeString(string(data))
	if err != nil || len(key) != 32 {
		return fmt.Errorf("%s must be raw standard base64 for exactly 32 bytes", label)
	}
	if weakApplicationKey(key) {
		return fmt.Errorf("%s is a known or repeated default", label)
	}
	return nil
}
func weakApplicationKey(key []byte) bool {
	if len(key) != 32 || bytes.Count(key, key[:1]) == len(key) {
		return true
	}
	for _, known := range [][]byte{[]byte("0123456789abcdef0123456789abcdef"), []byte("00000000000000000000000000000000"), []byte("changemechangemechangemechangeme")} {
		if bytes.Equal(key, known) {
			return true
		}
	}
	return false
}
func parseHTTPURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("must be an absolute URL without credentials or fragment")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("must use HTTP or HTTPS")
	}
	return u, nil
}
func loopbackHost(host string) bool {
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}
func splitCSV(raw string) []string {
	var out []string
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
func allowedHost(host string, allowed []string) bool {
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if h, _, err := net.SplitHostPort(candidate); err == nil {
			candidate = h
		}
		if strings.EqualFold(host, candidate) {
			return true
		}
	}
	return false
}
func parsePrefixes(raw string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, value := range splitCSV(raw) {
		p, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, fmt.Errorf("invalid trusted proxy network: %w", err)
		}
		out = append(out, p)
	}
	return out, nil
}

func serve(cfg Config) (result error) {
	ctx, cancel := signal.NotifyContext(context.Background(), terminationSignals()...)
	defer cancel()
	cleanupDevelopment, err := prepareDevelopment(&cfg)
	if err != nil {
		return err
	}
	defer cleanupDevelopment()
	database, release, err := store.OpenService(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer release()
	defer func() {
		result = finishService(result, func(ctx context.Context) error {
			var busy, log, done int
			err := database.DB().QueryRowContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)").Scan(&busy, &log, &done)
			if err == nil && busy != 0 {
				err = errors.New("checkpoint busy")
			}
			return err
		}, func(context.Context) error { return database.Close() })
	}()
	if err := database.Migrate(ctx); err != nil {
		return err
	}
	if cfg.Development {
		if err := seedDevelopment(ctx, database); err != nil {
			return err
		}
	}
	manager, err := newAuthManager(ctx, database, cfg, nil)
	if err != nil {
		return err
	}
	ready, err := readiness(ctx, database, cfg, manager)
	if err != nil {
		return err
	}
	documentService, wakeDocuments, stopDocuments, err := startDocuments(ctx, database, cfg)
	if err != nil {
		return err
	}
	defer func() {
		result = finishService(result, stopDocuments)
	}()
	logger := newApplicationLogger(cfg.Development, os.Stderr)
	slog.SetDefault(logger)
	handler, err := web.New(web.Dependencies{Documents: documentService, WakeDocuments: wakeDocuments, Auth: manager, Store: database, Logger: logger, Config: web.Config{DevAssetsDir: cfg.DevAssetsDir, AllowedHosts: cfg.AllowedHosts, TrustedProxies: cfg.TrustedProxies, Development: cfg.Development, BodyLimit: cfg.BodyLimit}})
	if err != nil {
		return err
	}
	handler = withHealth(handler, ready)
	server := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}}
	return serveHTTP(ctx, server, func() error {
		if cfg.TLSCertFile != "" {
			return server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
		}
		return server.ListenAndServe()
	})
}

func newAuthManager(ctx context.Context, database *store.Store, cfg Config, client *http.Client) (*auth.Manager, error) {
	return auth.NewManager(ctx, database, auth.Config{IssuerURL: cfg.PocketIDIssuer, ClientID: cfg.PocketIDClientID, ClientSecretFile: cfg.ClientSecretFile, RedirectURL: cfg.CallbackURL, SessionKeyFile: cfg.SessionKeyFile, TransactionKeyFile: cfg.TransactionKeyFile, RequiredGroup: cfg.RequiredGroup, Development: cfg.Development, HTTPClient: client})
}

func revokeAllSessions(ctx context.Context, path string) error {
	database, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }() // The readiness query reports its own errors.
	if err := database.Migrate(ctx); err != nil {
		return err
	}
	_, err = database.DB().ExecContext(ctx, "DELETE FROM sessions")
	return err
}

func revokeSessionByID(ctx context.Context, path, id string) error {
	database, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	if err := database.Migrate(ctx); err != nil {
		return err
	}
	deleted, err := database.AuthRepository().DeleteSessionByID(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return errors.New("session not found")
	}
	return nil
}
