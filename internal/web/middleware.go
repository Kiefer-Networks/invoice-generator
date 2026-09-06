package web

import (
	"context"
	"errors"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/auth"
)

type contextKey uint8

const (
	nonceKey contextKey = iota
	principalKey
	requestIDKey
	trustedPeerKey
)

func nonceFromContext(ctx context.Context) string {
	value, _ := ctx.Value(nonceKey).(string)
	return value
}
func principalFromContext(ctx context.Context) (auth.Principal, bool) {
	value, ok := ctx.Value(principalKey).(auth.Principal)
	return value, ok
}

func (a *app) chain(next http.Handler) http.Handler {
	return a.recover(a.correlation(a.proxy(a.transport(a.host(a.limit(a.security(a.log(a.session(next)))))))))
}
func (a *app) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() != nil {
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func (a *app) correlation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := randomNonce()
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, id)))
	})
}
func (a *app) proxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		normalized, err := normalizeProxy(r, a.config.TrustedProxies)
		if err != nil {
			http.Error(w, "invalid forwarded headers", http.StatusBadRequest)
			return
		}
		next.ServeHTTP(w, normalized)
	})
}
func (a *app) transport(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.config.Development || r.TLS != nil {
			next.ServeHTTP(w, r)
			return
		}
		trusted, _ := r.Context().Value(trustedPeerKey).(bool)
		if trusted && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
			next.ServeHTTP(w, r)
			return
		}
		http.Error(w, "HTTPS is required", http.StatusUpgradeRequired)
	})
}
func (a *app) host(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		for _, allowed := range a.config.AllowedHosts {
			if host == strings.ToLower(allowed) {
				next.ServeHTTP(w, r)
				return
			}
		}
		http.Error(w, "host not allowed", http.StatusMisdirectedRequest)
	})
}
func (a *app) limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > a.config.BodyLimit {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, a.config.BodyLimit)
		next.ServeHTTP(w, r)
	})
}
func (a *app) security(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nonce := randomNonce()
		r = r.WithContext(context.WithValue(r.Context(), nonceKey, nonce))
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-"+nonce+"'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; form-action 'self'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Permissions-Policy", "camera=(), geolocation=(), microphone=(), payment=()")
		if !strings.HasPrefix(r.URL.Path, "/assets/") && r.URL.Path != "/_health" {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
func (a *app) log(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		id, _ := r.Context().Value(requestIDKey).(string)
		a.logger.LogAttrs(r.Context(), slog.LevelInfo, "request", slog.String("request_id", id), slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Duration("duration", time.Since(started)))
	})
}
func (a *app) session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		session := cookie(r, "invoice_session")
		principal, err := a.auth.Authenticate(r.Context(), session)
		if err != nil {
			http.Redirect(w, r, "/auth/login?return_to="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			mediaType, _, parseErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if parseErr != nil || mediaType != "application/x-www-form-urlencoded" {
				http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
				return
			}
			if err := r.ParseForm(); err != nil {
				var maxErr *http.MaxBytesError
				if errors.As(err, &maxErr) {
					http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
				} else {
					http.Error(w, "invalid form", http.StatusBadRequest)
				}
				return
			}
			token := r.Header.Get("X-CSRF-Token")
			if token == "" {
				token = r.Form.Get("csrf_token")
			}
			if a.auth.ValidateCSRF(r.Context(), session, token) != nil {
				http.Error(w, "CSRF validation failed", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, principal)))
	})
}
func publicPath(path string) bool {
	return path == "/_health" || strings.HasPrefix(path, "/assets/") || path == "/auth/login" || path == "/auth/callback"
}
func normalizeProxy(r *http.Request, trusted []netip.Prefix) (*http.Request, error) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip, err := netip.ParseAddr(host)
	allowed := false
	if err == nil {
		for _, prefix := range trusted {
			if prefix.Contains(ip) {
				allowed = true
				break
			}
		}
	}
	if !allowed {
		return r, nil
	}
	copy := r.Clone(r.Context())
	copy = copy.WithContext(context.WithValue(copy.Context(), trustedPeerKey, true))
	fields := r.Header.Values("X-Forwarded-For")
	if len(fields) == 0 {
		return copy, nil
	}
	var addresses []netip.Addr
	for _, field := range fields {
		for _, part := range strings.Split(field, ",") {
			value := strings.TrimSpace(part)
			if value == "" {
				return nil, errors.New("empty forwarded address")
			}
			address, err := netip.ParseAddr(value)
			if err != nil {
				return nil, errors.New("invalid forwarded address")
			}
			addresses = append(addresses, address)
		}
	}
	for i := len(addresses) - 1; i >= 0; i-- {
		address := addresses[i]
		hopTrusted := false
		for _, prefix := range trusted {
			if prefix.Contains(address) {
				hopTrusted = true
				break
			}
		}
		if !hopTrusted {
			copy.RemoteAddr = address.String()
			return copy, nil
		}
	}
	return nil, errors.New("forwarded chain has no client address")
}
