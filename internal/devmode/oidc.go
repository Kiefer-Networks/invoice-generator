//go:build !production

// Package devmode provides local fixtures. It never disables application auth.
package devmode

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-jose/go-jose/v4"
	"html/template"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"time"
)

const ClientID = "invoice-development"
const ClientSecret = "local-fixture-only"

type authorization struct {
	Nonce, Challenge, Identity string
	Expires                    time.Time
}
type OIDC struct {
	*httptest.Server
	mu       sync.Mutex
	codes    map[string]authorization
	tokens   map[string]authorization
	signer   jose.Signer
	key      jose.JSONWebKey
	callback string
}

func loopbackURL(raw string) bool {
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

// StartOIDC only creates an ephemeral IPv4 loopback listener and key. The exact
// callback is fixed by the caller; the provider cannot redirect outside it.
func StartOIDC(callback string) (*OIDC, error) {
	u, e := url.Parse(callback)
	if e != nil || !loopbackURL(callback) || u.Path != "/auth/callback" || u.RawQuery != "" {
		return nil, errors.New("fixture callback must be loopback HTTP /auth/callback")
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	jwk := jose.JSONWebKey{Key: key, KeyID: "development-ephemeral", Algorithm: "ES256", Use: "sig"}
	signer, e := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: jwk}, (&jose.SignerOptions{}).WithType("JWT"))
	if e != nil {
		return nil, e
	}
	p := &OIDC{codes: map[string]authorization{}, tokens: map[string]authorization{}, signer: signer, key: jwk.Public(), callback: callback}
	p.Server = httptest.NewServer(http.HandlerFunc(p.serve))
	return p, nil
}
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
func randomToken() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (p *OIDC) claims(a authorization) map[string]any {
	groups := []string{"viewers"}
	if a.Identity == "admin" {
		groups = []string{"invoice-admins"}
	}
	now := time.Now().Unix()
	return map[string]any{"iss": p.URL, "sub": "dev-" + a.Identity, "aud": ClientID, "azp": ClientID, "nonce": a.Nonce, "iat": now, "auth_time": now, "exp": now + 120, "name": "Development " + a.Identity, "email": a.Identity + "@example.invalid", "groups": groups}
}
func (p *OIDC) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; form-action 'self' "+p.callback+"; frame-ancestors 'none'; base-uri 'none'")
	switch r.URL.Path {
	case "/.well-known/openid-configuration":
		writeJSON(w, map[string]any{"issuer": p.URL, "authorization_endpoint": p.URL + "/authorize", "token_endpoint": p.URL + "/token", "userinfo_endpoint": p.URL + "/userinfo", "jwks_uri": p.URL + "/jwks", "response_types_supported": []string{"code"}, "subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"ES256"}, "code_challenge_methods_supported": []string{"S256"}})
	case "/jwks":
		writeJSON(w, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{p.key}})
	case "/authorize":
		q := r.URL.Query()
		if r.Method != "GET" || q.Get("client_id") != ClientID || q.Get("redirect_uri") != p.callback || q.Get("response_type") != "code" || q.Get("state") == "" || q.Get("nonce") == "" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" {
			http.Error(w, "invalid local authorization request", 400)
			return
		}
		identity := q.Get("identity")
		if identity == "" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_ = template.Must(template.New("login").Parse(`<!doctype html><html lang="en"><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Development Pocket ID</title></head><body><main><h1>Development Pocket ID</h1><p>LOCAL DEVELOPMENT — synthetic identities only</p><form method="get" action="/authorize">{{range $key,$values:=.}}{{range $values}}<input type="hidden" name="{{$key}}" value="{{.}}">{{end}}{{end}}<button name="identity" value="admin">Sign in as invoice admin</button><button name="identity" value="outsider">Sign in without required group</button></form></main></body></html>`)).Execute(w, q)
			return
		}
		if identity != "admin" && identity != "outsider" {
			http.Error(w, "invalid identity", 400)
			return
		}
		code := randomToken()
		p.mu.Lock()
		for k, v := range p.codes {
			if time.Now().After(v.Expires) {
				delete(p.codes, k)
			}
		}
		if len(p.codes) >= 256 {
			p.mu.Unlock()
			http.Error(w, "too many authorizations", 429)
			return
		}
		p.codes[code] = authorization{Nonce: q.Get("nonce"), Challenge: q.Get("code_challenge"), Identity: identity, Expires: time.Now().Add(2 * time.Minute)}
		p.mu.Unlock()
		dest, _ := url.Parse(p.callback)
		dest.RawQuery = url.Values{"code": {code}, "state": {q.Get("state")}}.Encode()
		http.Redirect(w, r, dest.String(), 302)
	case "/token":
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if r.ParseForm() != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		id, secret, ok := r.BasicAuth()
		if !ok {
			id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
		}
		if id != ClientID || secret != ClientSecret || r.Form.Get("grant_type") != "authorization_code" || r.Form.Get("redirect_uri") != p.callback {
			http.Error(w, "invalid client", 401)
			return
		}
		p.mu.Lock()
		a, ok := p.codes[r.Form.Get("code")]
		delete(p.codes, r.Form.Get("code"))
		p.mu.Unlock()
		hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || time.Now().After(a.Expires) || base64.RawURLEncoding.EncodeToString(hash[:]) != a.Challenge {
			http.Error(w, "invalid code or PKCE", 400)
			return
		}
		raw, _ := json.Marshal(p.claims(a))
		signed, e := p.signer.Sign(raw)
		if e != nil {
			http.Error(w, "signing failed", 500)
			return
		}
		jwt, e := signed.CompactSerialize()
		if e != nil {
			http.Error(w, "signing failed", 500)
			return
		}
		token := randomToken()
		p.mu.Lock()
		for k, v := range p.tokens {
			if time.Now().After(v.Expires) {
				delete(p.tokens, k)
			}
		}
		p.tokens[token] = a
		p.mu.Unlock()
		writeJSON(w, map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 120, "id_token": jwt})
	case "/userinfo":
		var token string
		_, _ = fmt.Sscanf(r.Header.Get("Authorization"), "Bearer %s", &token)
		p.mu.Lock()
		a, ok := p.tokens[token]
		p.mu.Unlock()
		if !ok || time.Now().After(a.Expires) {
			http.Error(w, "invalid token", 401)
			return
		}
		writeJSON(w, p.claims(a))
	default:
		http.NotFound(w, r)
	}
}
