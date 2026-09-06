// Package auth implements the Pocket ID login boundary and local sessions.
package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/kiefer-networks/invoice-generator/internal/store"
	"golang.org/x/oauth2"
)

const (
	requiredGroup         = "invoice-admins"
	maxOIDCDocumentBytes  = 1 << 20
	transactionLifetime   = 5 * time.Minute
	sessionLifetime       = 15 * time.Minute
	allowedIssuedAtFuture = 2 * time.Minute
)

type Config struct {
	IssuerURL, ClientID, ClientSecret, RedirectURL       string
	SessionKey, TransactionKey                           string
	ClientSecretFile, SessionKeyFile, TransactionKeyFile string
	RequiredGroup                                        string
	Development                                          bool
	HTTPClient                                           *http.Client
	Now                                                  func() time.Time
}

type Manager struct {
	repo                                 *store.AuthRepository
	provider                             oidcProvider
	issuer, clientID, redirectURL, group string
	sessionKey                           []byte
	transactionAEAD                      cipher.AEAD
	secureCookies                        bool
	now                                  func() time.Time
	transactionsMu                       sync.Mutex
	transactions                         map[[32]byte]time.Time
}

// Principal is the only application role: an authorized Pocket ID member.
type Principal struct{ UserID, Issuer, Subject, DisplayName, Email string }

type SessionResult struct {
	Principal                        Principal
	SessionCookie, TransactionCookie *http.Cookie
	CSRFToken, ReturnTo              string
}

type oidcProvider interface {
	AuthorizationURL(state, nonce, verifier string) string
	Exchange(context.Context, string, string) (string, error)
	Verify(context.Context, string) (*oidc.IDToken, error)
}

type discoveredProvider struct {
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	client   *http.Client
}

func (p discoveredProvider) AuthorizationURL(state, nonce, verifier string) string {
	return p.oauth.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce), oauth2.S256ChallengeOption(verifier))
}
func (p discoveredProvider) Exchange(ctx context.Context, code, verifier string) (string, error) {
	token, err := p.oauth.Exchange(oidc.ClientContext(ctx, p.client), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", err
	}
	value, ok := token.Extra("id_token").(string)
	if !ok || value == "" {
		return "", errors.New("token response has no id token")
	}
	return value, nil
}
func (p discoveredProvider) Verify(ctx context.Context, raw string) (*oidc.IDToken, error) {
	return p.verifier.Verify(ctx, raw)
}

func NewManager(ctx context.Context, database *store.Store, cfg Config) (*Manager, error) {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	issuer, err := validateIssuer(cfg.IssuerURL, cfg.Development)
	if err != nil {
		return nil, err
	}
	redirect, err := validateRedirect(cfg.RedirectURL, cfg.Development)
	if err != nil {
		return nil, err
	}
	clientSecret, err := configuredSecret(cfg.ClientSecret, cfg.ClientSecretFile, cfg.Development, "Pocket ID client secret")
	if err != nil {
		return nil, err
	}
	sessionKey, err := configuredSecret(cfg.SessionKey, cfg.SessionKeyFile, cfg.Development, "session key")
	if err != nil {
		return nil, err
	}
	transactionKey, err := configuredSecret(cfg.TransactionKey, cfg.TransactionKeyFile, cfg.Development, "transaction key")
	if err != nil {
		return nil, err
	}
	if cfg.ClientID == "" || clientSecret == "" {
		return nil, errors.New("Pocket ID client credentials are required")
	}
	if len(sessionKey) != 32 || len(transactionKey) != 32 {
		return nil, errors.New("session and transaction keys must each be 32 bytes")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	client = boundedClient(client)
	doc, err := discover(ctx, client, issuer)
	if err != nil {
		return nil, err
	}
	if err := validateDiscovery(doc, issuer); err != nil {
		return nil, err
	}
	keySet := oidc.NewRemoteKeySet(oidc.ClientContext(ctx, client), doc.JWKSURI)
	verifier := oidc.NewVerifier(doc.Issuer, keySet, &oidc.Config{ClientID: cfg.ClientID, SupportedSigningAlgs: doc.SigningAlgorithms})
	block, err := aes.NewCipher([]byte(transactionKey))
	if err != nil {
		return nil, fmt.Errorf("transaction cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("transaction AEAD: %w", err)
	}
	group := cfg.RequiredGroup
	if group == "" {
		group = requiredGroup
	}
	if group != requiredGroup {
		return nil, errors.New("only the invoice-admins group is supported")
	}
	manager := &Manager{provider: discoveredProvider{oauth: oauth2.Config{ClientID: cfg.ClientID, ClientSecret: clientSecret, RedirectURL: redirect.String(), Endpoint: oauth2.Endpoint{AuthURL: doc.AuthorizationEndpoint, TokenURL: doc.TokenEndpoint}, Scopes: []string{oidc.ScopeOpenID, "profile", "email", "groups"}}, verifier: verifier, client: client}, issuer: issuer.String(), clientID: cfg.ClientID, redirectURL: redirect.String(), group: group, sessionKey: []byte(sessionKey), transactionAEAD: aead, secureCookies: redirect.Scheme == "https", now: now, transactions: make(map[[32]byte]time.Time)}
	if database != nil {
		manager.repo = database.AuthRepository()
	}
	return manager, nil
}

func configuredSecret(value, filename string, development bool, label string) (string, error) {
	if filename != "" {
		info, err := os.Stat(filename)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", label, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("read %s: path is a directory", label)
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", label, err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	if value != "" && development {
		return value, nil
	}
	if value != "" {
		return "", fmt.Errorf("%s must be supplied from a protected file", label)
	}
	return "", fmt.Errorf("%s is required", label)
}

type discoveryDocument struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	SigningAlgorithms     []string `json:"id_token_signing_alg_values_supported"`
}

func discover(ctx context.Context, client *http.Client, issuer *url.URL) (discoveryDocument, error) {
	u := *issuer
	u.Path = path.Join(u.Path, ".well-known/openid-configuration")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return discoveryDocument{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return discoveryDocument{}, fmt.Errorf("Pocket ID discovery: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return discoveryDocument{}, fmt.Errorf("Pocket ID discovery status %d", resp.StatusCode)
	}
	if err := rejectStaleDiscovery(resp.Header, time.Now()); err != nil {
		return discoveryDocument{}, err
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxOIDCDocumentBytes+1))
	if err != nil {
		return discoveryDocument{}, errors.New("read Pocket ID discovery document")
	}
	if len(body) > maxOIDCDocumentBytes {
		return discoveryDocument{}, errors.New("Pocket ID discovery document is too large")
	}
	var doc discoveryDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return discoveryDocument{}, errors.New("invalid Pocket ID discovery document")
	}
	return doc, nil
}

func rejectStaleDiscovery(headers http.Header, now time.Time) error {
	cacheControl := headers.Get("Cache-Control")
	maxAge := -1
	for _, directive := range strings.Split(cacheControl, ",") {
		directive = strings.TrimSpace(directive)
		if strings.HasPrefix(directive, "max-age=") {
			value, err := strconv.Atoi(strings.TrimPrefix(directive, "max-age="))
			if err != nil || value < 0 {
				return errors.New("invalid Pocket ID discovery cache policy")
			}
			maxAge = value
		}
	}
	if maxAge < 0 {
		return nil
	}
	date, err := http.ParseTime(headers.Get("Date"))
	if err != nil {
		return errors.New("Pocket ID discovery cache policy has no valid date")
	}
	age := now.Sub(date)
	if ageHeader := headers.Get("Age"); ageHeader != "" {
		seconds, err := strconv.Atoi(ageHeader)
		if err != nil || seconds < 0 {
			return errors.New("invalid Pocket ID discovery age")
		}
		age += time.Duration(seconds) * time.Second
	}
	if age > time.Duration(maxAge)*time.Second {
		return errors.New("Pocket ID discovery document is stale")
	}
	return nil
}

func validateDiscovery(doc discoveryDocument, issuer *url.URL) error {
	if doc.Issuer != issuer.String() {
		return errors.New("Pocket ID discovery issuer does not exactly match configured issuer")
	}
	for _, value := range []string{doc.AuthorizationEndpoint, doc.TokenEndpoint, doc.JWKSURI} {
		endpoint, err := url.Parse(value)
		if err != nil || endpoint.Scheme == "" || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" || endpoint.Scheme != issuer.Scheme || endpoint.Host != issuer.Host {
			return errors.New("Pocket ID discovery endpoint must be same-origin")
		}
	}
	if len(doc.SigningAlgorithms) == 0 {
		return errors.New("Pocket ID discovery has no signing algorithms")
	}
	for _, algorithm := range doc.SigningAlgorithms {
		if !allowedSigningAlgorithm(algorithm) {
			return errors.New("Pocket ID discovery permits an unsupported signing algorithm")
		}
	}
	return nil
}

func allowedSigningAlgorithm(algorithm string) bool {
	switch algorithm {
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512", "EdDSA":
		return true
	}
	return false
}

func validateIssuer(raw string, development bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid Pocket ID issuer URL")
	}
	if u.Scheme != "https" && !(development && u.Scheme == "http" && isLoopback(u.Hostname())) {
		return nil, errors.New("Pocket ID issuer must use HTTPS")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	return u, nil
}
func validateRedirect(raw string, development bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("invalid fixed callback URL")
	}
	if u.Scheme != "https" && !(development && u.Scheme == "http" && isLoopback(u.Hostname())) {
		return nil, errors.New("callback URL must use HTTPS")
	}
	return u, nil
}
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
func boundedClient(base *http.Client) *http.Client {
	copy := *base
	copy.Transport = &boundedRoundTripper{base: base.Transport}
	return &copy
}

type boundedRoundTripper struct{ base http.RoundTripper }

func (r *boundedRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := r.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(io.LimitReader(resp.Body, maxOIDCDocumentBytes+1))
	return resp, nil
}

func randomValue() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

type transaction struct {
	State, Nonce, Verifier, ReturnTo string
	ExpiresAt                        time.Time
}

func (m *Manager) Begin(returnTo string) (string, *http.Cookie, error) {
	state, err := randomValue()
	if err != nil {
		return "", nil, err
	}
	nonce, err := randomValue()
	if err != nil {
		return "", nil, err
	}
	verifier, err := randomValue()
	if err != nil {
		return "", nil, err
	}
	t := transaction{State: state, Nonce: nonce, Verifier: verifier, ReturnTo: safeReturnTo(returnTo), ExpiresAt: m.now().UTC().Add(transactionLifetime)}
	encoded, err := m.sealTransaction(t)
	if err != nil {
		return "", nil, err
	}
	hash := sha256.Sum256([]byte(state))
	m.transactionsMu.Lock()
	m.transactions[hash] = t.ExpiresAt
	m.transactionsMu.Unlock()
	return m.provider.AuthorizationURL(state, nonce, verifier), m.transactionCookie(encoded, transactionLifetime), nil
}

func (m *Manager) Callback(ctx context.Context, callbackURL *url.URL, cookie *http.Cookie) (SessionResult, error) {
	deleted := m.transactionCookie("", -1)
	if callbackURL == nil || callbackURL.Scheme+"://"+callbackURL.Host+callbackURL.Path != m.redirectURL {
		return SessionResult{TransactionCookie: deleted}, errors.New("callback URL does not match configured redirect URL")
	}
	q := callbackURL.Query()
	if hasDuplicate(q, "state") || hasDuplicate(q, "code") || hasDuplicate(q, "error") {
		return SessionResult{TransactionCookie: deleted}, errors.New("callback has duplicate OAuth parameters")
	}
	if q.Get("error") != "" {
		return SessionResult{TransactionCookie: deleted}, errors.New("Pocket ID authorization was denied")
	}
	state := q.Get("state")
	code := q.Get("code")
	if state == "" || code == "" {
		return SessionResult{TransactionCookie: deleted}, errors.New("callback has no authorization code or state")
	}
	t, err := m.openTransaction(cookie)
	if err != nil {
		return SessionResult{TransactionCookie: deleted}, err
	}
	if t.State != state || !m.consumeTransaction(state) {
		return SessionResult{TransactionCookie: deleted}, errors.New("invalid or replayed authorization transaction")
	}
	if !m.now().Before(t.ExpiresAt) {
		return SessionResult{TransactionCookie: deleted}, errors.New("expired authorization transaction")
	}
	raw, err := m.provider.Exchange(ctx, code, t.Verifier)
	if err != nil {
		return SessionResult{TransactionCookie: deleted}, errors.New("Pocket ID token exchange failed")
	}
	token, err := m.provider.Verify(ctx, raw)
	if err != nil {
		return SessionResult{TransactionCookie: deleted}, errors.New("Pocket ID id token verification failed")
	}
	identity, err := m.verifyIdentity(token, t.Nonce)
	if err != nil {
		return SessionResult{TransactionCookie: deleted}, err
	}
	if m.repo == nil {
		return SessionResult{TransactionCookie: deleted}, errors.New("authentication repository is required")
	}
	user, err := m.repo.UpsertUser(ctx, store.OIDCUser{Issuer: m.issuer, Subject: identity.Subject, DisplayName: identity.Name, Email: identity.Email, LastLoginAt: m.now(), LastAuthorizationAt: m.now()})
	if err != nil {
		return SessionResult{TransactionCookie: deleted}, errors.New("store Pocket ID identity")
	}
	sessionCookie, csrf, err := m.createSession(ctx, user)
	if err != nil {
		return SessionResult{TransactionCookie: deleted}, err
	}
	return SessionResult{Principal: principal(user), SessionCookie: sessionCookie, TransactionCookie: deleted, CSRFToken: csrf, ReturnTo: t.ReturnTo}, nil
}

type identityClaims struct {
	Subject            string   `json:"sub"`
	Name               string   `json:"name"`
	Email              string   `json:"email"`
	Nonce              string   `json:"nonce"`
	AuthorizedParty    string   `json:"azp"`
	IssuedAt           int64    `json:"iat"`
	AuthenticationTime int64    `json:"auth_time"`
	Groups             []string `json:"groups"`
}

func (m *Manager) verifyIdentity(token *oidc.IDToken, nonce string) (identityClaims, error) {
	var c identityClaims
	if err := token.Claims(&c); err != nil {
		return c, errors.New("invalid Pocket ID id token claims")
	}
	if c.Subject == "" || c.Nonce != nonce {
		return c, errors.New("Pocket ID id token subject or nonce is invalid")
	}
	if c.AuthorizedParty != "" && c.AuthorizedParty != m.clientID {
		return c, errors.New("Pocket ID id token authorized party is invalid")
	}
	now := m.now()
	if c.IssuedAt == 0 || time.Unix(c.IssuedAt, 0).After(now.Add(allowedIssuedAtFuture)) || c.AuthenticationTime == 0 || time.Unix(c.AuthenticationTime, 0).After(now.Add(allowedIssuedAtFuture)) || time.Unix(c.AuthenticationTime, 0).Before(now.Add(-sessionLifetime)) {
		return c, errors.New("Pocket ID id token time claims are invalid")
	}
	allowed := false
	for _, group := range c.Groups {
		if group == m.group {
			allowed = true
			break
		}
	}
	if !allowed {
		return c, errors.New("Pocket ID identity is not in invoice-admins")
	}
	return c, nil
}

func (m *Manager) createSession(ctx context.Context, user store.OIDCUser) (*http.Cookie, string, error) {
	token, err := randomValue()
	if err != nil {
		return nil, "", err
	}
	csrf, err := randomValue()
	if err != nil {
		return nil, "", err
	}
	expires := m.now().UTC().Add(sessionLifetime)
	session := store.StoredSession{ID: "", UserID: user.ID, TokenHash: m.keyedHash(token), CSRFSecretHash: m.keyedHash(csrf), AuthorizationExpiresAt: expires, ExpiresAt: expires}
	session.ID, err = newID()
	if err != nil {
		return nil, "", err
	}
	if err := m.repo.DeleteExpiredSessions(ctx, m.now(), 100); err != nil {
		return nil, "", err
	}
	if err := m.repo.CreateSession(ctx, session); err != nil {
		return nil, "", err
	}
	return m.sessionCookie(token+"."+csrf, sessionLifetime), csrf, nil
}
func (m *Manager) Authenticate(ctx context.Context, cookie *http.Cookie) (Principal, error) {
	token, _, ok := splitSessionCookie(cookie)
	if !ok {
		return Principal{}, errors.New("invalid session")
	}
	session, user, err := m.repo.SessionByTokenHash(ctx, m.keyedHash(token), m.now())
	if err != nil {
		return Principal{}, errors.New("invalid or expired session")
	}
	_ = session
	return principal(user), nil
}
func (m *Manager) Logout(ctx context.Context, cookie *http.Cookie) error {
	token, _, ok := splitSessionCookie(cookie)
	if !ok {
		return nil
	}
	return m.repo.DeleteSessionByTokenHash(ctx, m.keyedHash(token))
}
func (m *Manager) RevokeAll(ctx context.Context, p Principal) error {
	return m.repo.DeleteSessionsForUser(ctx, p.UserID)
}
func (m *Manager) CSRFToken(cookie *http.Cookie) (string, error) {
	_, csrf, ok := splitSessionCookie(cookie)
	if !ok {
		return "", errors.New("invalid session")
	}
	return csrf, nil
}
func (m *Manager) ValidateCSRF(ctx context.Context, cookie *http.Cookie, token string) error {
	sessionToken, _, ok := splitSessionCookie(cookie)
	if !ok || token == "" {
		return errors.New("invalid csrf token")
	}
	session, _, err := m.repo.SessionByTokenHash(ctx, m.keyedHash(sessionToken), m.now())
	if err != nil {
		return errors.New("invalid csrf token")
	}
	expected := m.keyedHash(token)
	if subtle.ConstantTimeCompare(session.CSRFSecretHash, expected) != 1 {
		return errors.New("invalid csrf token")
	}
	return nil
}
func principal(user store.OIDCUser) Principal {
	return Principal{UserID: user.ID, Issuer: user.Issuer, Subject: user.Subject, DisplayName: user.DisplayName, Email: user.Email}
}
func (m *Manager) keyedHash(value string) []byte {
	h := hmac.New(sha256.New, m.sessionKey)
	_, _ = h.Write([]byte(value))
	return h.Sum(nil)
}
func (m *Manager) transactionCookie(value string, lifetime time.Duration) *http.Cookie {
	maxAge := int(lifetime.Seconds())
	if lifetime < 0 {
		maxAge = -1
	}
	return &http.Cookie{Name: transactionCookieName, Value: value, Path: "/", MaxAge: maxAge, Expires: m.now().Add(lifetime), HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteLaxMode}
}
func (m *Manager) sessionCookie(value string, lifetime time.Duration) *http.Cookie {
	return &http.Cookie{Name: sessionCookieName, Value: value, Path: "/", MaxAge: int(lifetime.Seconds()), Expires: m.now().Add(lifetime), HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteLaxMode}
}
func safeReturnTo(value string) string {
	if value == "" {
		return "/"
	}
	u, err := url.Parse(value)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, "/") || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	return u.RequestURI()
}
func (m *Manager) sealTransaction(t transaction) (string, error) {
	plain, err := json.Marshal(t)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, m.transactionAEAD.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(append(nonce, m.transactionAEAD.Seal(nil, nonce, plain, nil)...)), nil
}
func (m *Manager) openTransaction(cookie *http.Cookie) (transaction, error) {
	if cookie == nil || cookie.Name != transactionCookieName || cookie.Value == "" {
		return transaction{}, errors.New("missing authorization transaction")
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(raw) < m.transactionAEAD.NonceSize() {
		return transaction{}, errors.New("invalid authorization transaction")
	}
	plain, err := m.transactionAEAD.Open(nil, raw[:m.transactionAEAD.NonceSize()], raw[m.transactionAEAD.NonceSize():], nil)
	if err != nil {
		return transaction{}, errors.New("invalid authorization transaction")
	}
	var t transaction
	if json.Unmarshal(plain, &t) != nil || t.State == "" || t.Nonce == "" || t.Verifier == "" {
		return transaction{}, errors.New("invalid authorization transaction")
	}
	return t, nil
}
func (m *Manager) consumeTransaction(state string) bool {
	hash := sha256.Sum256([]byte(state))
	m.transactionsMu.Lock()
	defer m.transactionsMu.Unlock()
	expiry, ok := m.transactions[hash]
	delete(m.transactions, hash)
	return ok && m.now().Before(expiry)
}
func hasDuplicate(values url.Values, key string) bool { return len(values[key]) > 1 }
func splitSessionCookie(cookie *http.Cookie) (string, string, bool) {
	if cookie == nil || cookie.Name != sessionCookieName {
		return "", "", false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
