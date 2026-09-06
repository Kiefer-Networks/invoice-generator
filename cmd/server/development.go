//go:build !production

package main

import (
	"errors"
	"flag"
	"github.com/kiefer-networks/invoice-generator/internal/devmode"
	"io"
	"net"
	"path/filepath"
	"strings"
)

func parseDevelopment(args []string, getenv func(string) string) (Config, error) {
	// Inherited deployment settings are a configuration error, never a fallback.
	for _, key := range []string{"DATABASE", "DOCUMENT_ROOT", "POCKET_ID_ISSUER", "POCKET_ID_CLIENT_ID", "POCKET_ID_CLIENT_SECRET_FILE", "SESSION_KEY_FILE", "TRANSACTION_KEY_FILE", "PAPERLESS_URL", "PAPERLESS_TOKEN_FILE", "CALLBACK_URL", "TLS_CERT", "TLS_KEY", "TRUSTED_PROXIES", "ALLOWED_HOSTS", "LISTEN"} {
		if strings.TrimSpace(getenv("INVOICE_"+key)) != "" {
			return Config{}, errors.New("development refuses inherited deployment configuration")
		}
	}
	fs := flag.NewFlagSet("serve -dev", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	dev := fs.Bool("dev", false, "explicit local development")
	root := fs.String("dev-root", ".invoice-development", "isolated development root")
	listen := fs.String("listen", "127.0.0.1:8080", "literal loopback address")
	assets := fs.String("dev-assets", "", "dedicated development templates/static directory")
	state := fs.String("dev-paperless", "accepted", "accepted, delayed, rejected, timeout, reject-once")
	if e := fs.Parse(args); e != nil {
		return Config{}, errors.New("development accepts only -dev, -dev-root, -listen and -dev-paperless")
	}
	if !*dev || fs.NArg() != 0 {
		return Config{}, errors.New("explicit -dev required")
	}
	absolute, e := filepath.Abs(*root)
	if e != nil {
		return Config{}, e
	}
	cfg := Config{DevAssetsDir: *assets, Development: true, DevRoot: absolute, DevPaperlessState: *state, Listen: *listen, Database: filepath.Join(absolute, "development.sqlite"), DocumentRoot: filepath.Join(absolute, "documents"), BodyLimit: defaultBodyLimit, RequiredGroup: "invoice-admins"}
	host, _, e := net.SplitHostPort(*listen)
	if e != nil {
		return Config{}, e
	}
	cfg.AllowedHosts = []string{host}
	cfg.CallbackURL = "http://" + *listen + "/auth/callback"
	return cfg, cfg.Validate()
}
func validateDevelopment(c Config) error {
	host, port, e := net.SplitHostPort(c.Listen)
	ip := net.ParseIP(host)
	if e != nil || ip == nil || !ip.IsLoopback() || port == "0" {
		return errors.New("development listener must be a literal loopback with a fixed port")
	}
	if !filepath.IsAbs(c.DevRoot) || c.Database != filepath.Join(c.DevRoot, "development.sqlite") || c.DocumentRoot != filepath.Join(c.DevRoot, "documents") {
		return errors.New("development data must remain under its isolated root")
	}
	if c.PocketIDIssuer != "" || c.PocketIDClientID != "" || c.ClientSecretFile != "" || c.SessionKeyFile != "" || c.TransactionKeyFile != "" || c.PaperlessURL != "" || c.PaperlessTokenFile != "" || c.TLSCertFile != "" || c.TLSKeyFile != "" || len(c.TrustedProxies) != 0 {
		return errors.New("development providers and secrets must be provisioned locally")
	}
	if c.RequiredGroup != "invoice-admins" || c.BodyLimit != defaultBodyLimit || len(c.AllowedHosts) != 1 || c.AllowedHosts[0] != host || c.CallbackURL != "http://"+c.Listen+"/auth/callback" || !devmode.ValidPaperlessState(c.DevPaperlessState) {
		return errors.New("invalid isolated development configuration")
	}
	return devmode.ValidateRoot(c.DevRoot)
}
