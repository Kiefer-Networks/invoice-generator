package web

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	defaultAuthRateLimit       = 10
	defaultAuthRateWindow      = time.Minute
	defaultAuthRateClients     = 4096
	defaultExpensiveConcurrent = 2
)

type clientWindow struct {
	started time.Time
	count   int
}

type clientRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	max     int
	now     func() time.Time
	clients map[string]clientWindow
}

func newClientRateLimiter(limit int, window time.Duration, max int, now func() time.Time) *clientRateLimiter {
	return &clientRateLimiter{limit: limit, window: window, max: max, now: now, clients: make(map[string]clientWindow)}
}

func (l *clientRateLimiter) allow(client string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	entry, exists := l.clients[client]
	if exists && now.Sub(entry.started) >= l.window {
		delete(l.clients, client)
		exists = false
	}
	if !exists {
		if len(l.clients) >= l.max {
			var oldestKey string
			var oldest time.Time
			for key, candidate := range l.clients {
				if oldestKey == "" || candidate.started.Before(oldest) {
					oldestKey, oldest = key, candidate.started
				}
			}
			delete(l.clients, oldestKey)
		}
		l.clients[client] = clientWindow{started: now, count: 1}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	l.clients[client] = entry
	return true
}

func clientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func authEndpoint(r *http.Request) bool {
	return r.Method == http.MethodGet && (r.URL.Path == "/auth/login" || r.URL.Path == "/auth/callback")
}

func expensiveEndpoint(r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/invoices/") {
		return false
	}
	return (r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/preview")) || (r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/finalize"))
}

func (a *app) resources(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if authEndpoint(r) && !a.authLimiter.allow(clientAddress(r)) {
			w.Header().Set("Retry-After", "60")
			http.Error(w, "too many authentication requests", http.StatusTooManyRequests)
			return
		}
		if !expensiveEndpoint(r) {
			next.ServeHTTP(w, r)
			return
		}
		select {
		case a.expensive <- struct{}{}:
			defer func() { <-a.expensive }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			http.Error(w, "operation capacity is temporarily exhausted", http.StatusServiceUnavailable)
		}
	})
}
