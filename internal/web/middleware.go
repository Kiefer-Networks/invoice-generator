package web

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/auth"
)

type contextKey uint8

const (
	nonceKey contextKey = iota
	principalKey
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
	return a.recover(a.correlation(a.proxy(a.host(a.limit(a.security(a.log(a.session(next))))))))
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
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey(99), id)))
	})
}
func (a *app) proxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, normalizeProxy(r, a.config.TrustedProxies))
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
		a.logger.LogAttrs(r.Context(), slog.LevelInfo, "request", slog.String("method", r.Method), slog.String("path", r.URL.Path), slog.Duration("duration", time.Since(started)))
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
			http.Redirect(w, r, "/auth/login?return_to="+r.URL.RequestURI(), http.StatusFound)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
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
func normalizeProxy(r *http.Request, trusted []netip.Prefix) *http.Request {
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
		return r
	}
	copy := r.Clone(r.Context())
	value := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-For"), ",")[0])
	if _, err := netip.ParseAddr(value); err == nil {
		copy.RemoteAddr = value
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		copy.URL.Scheme = "https"
	}
	return copy
}
