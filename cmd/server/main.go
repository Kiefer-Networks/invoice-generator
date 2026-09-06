// Command server runs the authenticated invoice administration application.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/auth"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"github.com/kiefer-networks/invoice-generator/internal/web"
)

const defaultBodyLimit int64 = 1 << 20

// Config contains the deployment boundary for the HTTP service. Secrets are
// represented only by file paths so they cannot accidentally reach logs.
type Config struct {
	Listen, Database                                     string
	AllowedHosts                                         []string
	TrustedProxies                                       []netip.Prefix
	TLSCertFile, TLSKeyFile                              string
	Development                                          bool
	BodyLimit                                            int64
	PocketIDIssuer, PocketIDClientID, CallbackURL        string
	ClientSecretFile, SessionKeyFile, TransactionKeyFile string
	RequiredGroup, PaperlessURL                          string
}

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "sessions" && os.Args[2] == "revoke-all" {
		fs := flag.NewFlagSet("sessions revoke-all", flag.ContinueOnError)
		database := fs.String("database", strings.TrimSpace(os.Getenv("INVOICE_DATABASE")), "SQLite database")
		if err := fs.Parse(os.Args[3:]); err != nil || *database == "" {
			fmt.Fprintln(os.Stderr, "usage: server sessions revoke-all -database PATH")
			os.Exit(2)
		}
		if err := revokeAllSessions(context.Background(), *database); err != nil {
			fmt.Fprintln(os.Stderr, "server:", err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) < 2 || os.Args[1] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: server serve [flags]")
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

// ParseConfig reads non-secret configuration from flags and environment.
func ParseConfig(args []string, getenv func(string) string) (Config, error) {
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
	hosts := fs.String("allowed-hosts", value("INVOICE_ALLOWED_HOSTS", ""), "comma-separated hosts")
	proxies := fs.String("trusted-proxies", value("INVOICE_TRUSTED_PROXIES", ""), "comma-separated proxy CIDRs")
	cert := fs.String("tls-cert", value("INVOICE_TLS_CERT", ""), "TLS certificate file")
	key := fs.String("tls-key", value("INVOICE_TLS_KEY", ""), "TLS key file")
	dev := fs.Bool("dev", value("INVOICE_DEVELOPMENT", "") == "1", "development mode")
	issuer := fs.String("pocket-id-issuer", value("INVOICE_POCKET_ID_ISSUER", ""), "Pocket ID issuer")
	clientID := fs.String("pocket-id-client-id", value("INVOICE_POCKET_ID_CLIENT_ID", ""), "Pocket ID client ID")
	clientSecret := fs.String("pocket-id-client-secret-file", value("INVOICE_POCKET_ID_CLIENT_SECRET_FILE", ""), "Pocket ID client secret file")
	sessionKey := fs.String("session-key-file", value("INVOICE_SESSION_KEY_FILE", ""), "session key file")
	transactionKey := fs.String("transaction-key-file", value("INVOICE_TRANSACTION_KEY_FILE", ""), "transaction key file")
	callback := fs.String("callback-url", value("INVOICE_CALLBACK_URL", ""), "fixed Pocket ID callback URL")
	group := fs.String("required-group", value("INVOICE_REQUIRED_GROUP", "invoice-admins"), "required Pocket ID group")
	paperless := fs.String("paperless-url", value("INVOICE_PAPERLESS_URL", ""), "Paperless URL")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	trusted, err := parsePrefixes(*proxies)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Listen: *listen, Database: *database, AllowedHosts: splitCSV(*hosts), TrustedProxies: trusted, TLSCertFile: *cert, TLSKeyFile: *key, Development: *dev, BodyLimit: defaultBodyLimit, PocketIDIssuer: *issuer, PocketIDClientID: *clientID, ClientSecretFile: *clientSecret, SessionKeyFile: *sessionKey, TransactionKeyFile: *transactionKey, CallbackURL: *callback, RequiredGroup: *group, PaperlessURL: *paperless}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
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
		return fmt.Errorf("Pocket ID issuer: %w", err)
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
	_, session, transaction, err := c.readSecrets()
	if err != nil {
		return err
	}
	if len(session) != 32 || len(transaction) != 32 {
		return errors.New("session and transaction keys must be exactly 32 bytes")
	}
	if bytes.Equal(session, transaction) || defaultKey(session) || defaultKey(transaction) {
		return errors.New("application keys must be distinct non-default values")
	}
	return nil
}

func (c Config) readSecrets() (string, []byte, []byte, error) {
	client, err := readSecret(c.ClientSecretFile)
	if err != nil || strings.TrimSpace(string(client)) == "" {
		return "", nil, nil, errors.New("Pocket ID client secret file is required")
	}
	session, err := readSecret(c.SessionKeyFile)
	if err != nil {
		return "", nil, nil, errors.New("session key file is required")
	}
	transaction, err := readSecret(c.TransactionKeyFile)
	if err != nil {
		return "", nil, nil, errors.New("transaction key file is required")
	}
	return strings.TrimSpace(string(client)), bytes.TrimSpace(session), bytes.TrimSpace(transaction), nil
}

func readSecret(path string) ([]byte, error) {
	if path == "" {
		return nil, errors.New("missing secret file")
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("invalid secret file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&007 != 0 {
		return nil, errors.New("secret file is accessible by group or others")
	}
	return os.ReadFile(path)
}
func defaultKey(key []byte) bool { return len(key) == 0 || bytes.Count(key, key[:1]) == len(key) }
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

func serve(cfg Config) error {
	ctx := context.Background()
	database, err := store.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		return err
	}
	clientSecret, sessionKey, transactionKey, err := cfg.readSecrets()
	if err != nil {
		return err
	}
	manager, err := auth.NewManager(ctx, database, auth.Config{IssuerURL: cfg.PocketIDIssuer, ClientID: cfg.PocketIDClientID, ClientSecret: clientSecret, RedirectURL: cfg.CallbackURL, SessionKey: string(sessionKey), TransactionKey: string(transactionKey), RequiredGroup: cfg.RequiredGroup, Development: cfg.Development})
	if err != nil {
		return err
	}
	handler, err := web.New(web.Dependencies{Auth: manager, Store: database, Config: web.Config{AllowedHosts: cfg.AllowedHosts, TrustedProxies: cfg.TrustedProxies, Development: cfg.Development, BodyLimit: cfg.BodyLimit}})
	if err != nil {
		return err
	}
	server := &http.Server{Addr: cfg.Listen, Handler: handler, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 1 << 20, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS13}}
	if cfg.TLSCertFile != "" {
		return server.ListenAndServeTLS(cfg.TLSCertFile, cfg.TLSKeyFile)
	}
	return server.ListenAndServe()
}

func revokeAllSessions(ctx context.Context, path string) error {
	database, err := store.Open(ctx, path)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		return err
	}
	_, err = database.DB().ExecContext(ctx, "DELETE FROM sessions")
	return err
}
