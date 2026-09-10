//go:build !production

package devmode

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
)

func TestDevOIDCDiscoveryAndRejectForeignCallback(t *testing.T) {
	p, e := StartOIDC("http://127.0.0.1:8080/auth/callback")
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	provider, e := oidc.NewProvider(context.Background(), p.URL)
	if e != nil {
		t.Fatal(e)
	}
	if provider.Endpoint().TokenURL != p.URL+"/token" {
		t.Fatal("missing token endpoint")
	}
	r, e := http.Get(p.URL + "/authorize?client_id=invoice-development&redirect_uri=" + url.QueryEscape("https://example.com/auth/callback"))
	if e != nil {
		t.Fatal(e)
	}
	_ = r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatal("foreign callback accepted")
	}
	if _, e = StartOIDC("http://0.0.0.0:8080/auth/callback"); e == nil {
		t.Fatal("non-loopback callback accepted")
	}
}
