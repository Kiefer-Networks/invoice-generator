package auth

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReadinessRevalidatesDiscovery(t *testing.T) {
	var broken atomic.Bool
	server := discoveryServer(t, func(issuer string) string {
		if broken.Load() {
			return `{}`
		}
		return validDiscovery(issuer)
	}, nil)
	m, err := NewManager(context.Background(), nil, Config{IssuerURL: server.URL, ClientID: "client", ClientSecret: "secret", RedirectURL: "https://app.example.test/auth/callback", SessionKey: strings.Repeat("s", 32), TransactionKey: strings.Repeat("t", 32), Development: true, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.CheckDiscovery(context.Background()); err != nil {
		t.Fatal(err)
	}
	broken.Store(true)
	if m.CheckDiscovery(context.Background()) == nil {
		t.Fatal("readiness accepted changed invalid discovery")
	}
}
