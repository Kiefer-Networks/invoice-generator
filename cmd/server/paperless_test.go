package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestPaperlessServerConfigAndMissingToken(t *testing.T) {
	cfg := testConfig(t)
	cfg.PaperlessURL = "http://paperless.internal"
	if e := cfg.Validate(); e == nil {
		t.Fatal("insecure URL")
	}
	cfg.PaperlessURL = "https://paperless.internal"
	cfg.PaperlessTokenFile = filepath.Join(t.TempDir(), "not-mounted-yet")
	if e := cfg.Validate(); e != nil {
		t.Fatal(e)
	}
	s, e := store.Open(context.Background(), cfg.Database)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if e = s.Migrate(context.Background()); e != nil {
		t.Fatal(e)
	}
	_, _, stop, e := startDocuments(context.Background(), s, cfg)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = stop(ctx); e != nil {
		t.Fatal(e)
	}
}

func TestPaperlessDevelopmentCannotReadProductionToken(t *testing.T) {
	cfg := testConfig(t)
	cfg.Development = true
	cfg.PocketIDIssuer = "http://127.0.0.1:1234"
	cfg.CallbackURL = "http://127.0.0.1/auth/callback"
	cfg.AllowedHosts = []string{"127.0.0.1"}
	cfg.PaperlessTokenFile = writeSecret(t, "token")
	if e := cfg.Validate(); e == nil {
		t.Fatal("development accepted production token")
	}
}
