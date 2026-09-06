//go:build !production

package main

import (
	"context"
	"fmt"
	"github.com/kiefer-networks/invoice-generator/internal/devmode"
	"github.com/kiefer-networks/invoice-generator/internal/paperless"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"net/http"
	"os"
	"time"
)

func prepareDevelopment(cfg *Config) (func(), error) {
	noop := func() {}
	if !cfg.Development {
		return noop, nil
	}
	if err := cfg.Validate(); err != nil {
		return noop, err
	}
	secrets, err := devmode.PrepareRoot(cfg.DevRoot)
	if err != nil {
		return noop, err
	}
	provider, err := devmode.StartOIDC(cfg.CallbackURL)
	if err != nil {
		return noop, err
	}
	remote, err := devmode.StartPaperless(cfg.DevPaperlessState)
	if err != nil {
		provider.Close()
		return noop, err
	}
	cfg.PocketIDIssuer, cfg.PocketIDClientID = provider.URL, devmode.ClientID
	cfg.ClientSecretFile, cfg.SessionKeyFile, cfg.TransactionKeyFile = secrets.Client, secrets.Session, secrets.Transaction
	cfg.devPaperless = func() (*paperless.Client, error) {
		return paperless.NewClient(paperless.Config{URL: remote.URL, APIKey: devmode.PaperlessToken}, &http.Client{Timeout: time.Second}, true)
	}
	fmt.Fprintln(os.Stdout, "LOCAL DEVELOPMENT — synthetic data only — http://"+cfg.Listen)
	return func() { remote.Close(); provider.Close() }, nil
}
func seedDevelopment(ctx context.Context, db *store.Store) error { return devmode.Seed(ctx, db) }
