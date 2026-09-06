//go:build !production

package devmode

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/coreos/go-oidc/v3/oidc"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDevOIDCSignedTokensPKCEReplayAndUserinfo(t *testing.T) {
	p, e := StartOIDC("http://127.0.0.1:8080/auth/callback")
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	provider, e := oidc.NewProvider(context.Background(), p.URL)
	if e != nil {
		t.Fatal(e)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	verifier := strings.Repeat("a", 43)
	hash := sha256.Sum256([]byte(verifier))
	authorize := func(identity string) string {
		t.Helper()
		q := url.Values{"client_id": {ClientID}, "redirect_uri": {p.callback}, "response_type": {"code"}, "state": {"fixed-state"}, "nonce": {"fixed-nonce"}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(hash[:])}, "identity": {identity}}
		r, e := client.Get(p.URL + "/authorize?" + q.Encode())
		if e != nil {
			t.Fatal(e)
		}
		defer r.Body.Close()
		if r.StatusCode != 302 {
			t.Fatalf("authorize %d", r.StatusCode)
		}
		u, e := url.Parse(r.Header.Get("Location"))
		if e != nil || u.Query().Get("state") != "fixed-state" {
			t.Fatal("state was not returned")
		}
		return u.Query().Get("code")
	}
	exchange := func(code, proof string) *http.Response {
		t.Helper()
		r, e := http.PostForm(p.URL+"/token", url.Values{"client_id": {ClientID}, "client_secret": {ClientSecret}, "redirect_uri": {p.callback}, "grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {proof}})
		if e != nil {
			t.Fatal(e)
		}
		return r
	}
	for _, identity := range []string{"admin", "outsider"} {
		code := authorize(identity)
		r := exchange(code, verifier)
		var tokens struct {
			ID     string `json:"id_token"`
			Access string `json:"access_token"`
		}
		e = json.NewDecoder(r.Body).Decode(&tokens)
		r.Body.Close()
		if e != nil || r.StatusCode != 200 {
			t.Fatal("token exchange failed", e)
		}
		token, e := provider.Verifier(&oidc.Config{ClientID: ClientID}).Verify(context.Background(), tokens.ID)
		if e != nil {
			t.Fatal(e)
		}
		var claims struct {
			Nonce  string   `json:"nonce"`
			Groups []string `json:"groups"`
		}
		if e = token.Claims(&claims); e != nil {
			t.Fatal(e)
		}
		if claims.Nonce != "fixed-nonce" || time.Until(token.Expiry) > 2*time.Minute || len(claims.Groups) != 1 || (identity == "admin") != (claims.Groups[0] == "invoice-admins") {
			t.Fatal("incorrect signed claims")
		}
		req, _ := http.NewRequest("GET", p.URL+"/userinfo", nil)
		req.Header.Set("Authorization", "Bearer "+tokens.Access)
		user, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		var info map[string]any
		e = json.NewDecoder(user.Body).Decode(&info)
		user.Body.Close()
		if e != nil || info["sub"] != "dev-"+identity {
			t.Fatal("userinfo mismatch")
		}
		replay := exchange(code, verifier)
		replay.Body.Close()
		if replay.StatusCode != 400 {
			t.Fatal("authorization code replay accepted")
		}
	}
	code := authorize("admin")
	rejected := exchange(code, "wrong-verifier")
	rejected.Body.Close()
	if rejected.StatusCode != 400 {
		t.Fatal("invalid PKCE accepted")
	}
	replay := exchange(code, verifier)
	replay.Body.Close()
	if replay.StatusCode != 400 {
		t.Fatal("failed code was reusable")
	}
}
