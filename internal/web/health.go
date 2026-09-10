package web

import (
	"context"
	"net/http"
	"time"
)

// HealthHandler exposes fixed, dependency-detail-free responses. Mount it before
// authentication only for the two exact paths; readiness must fail closed.
func HealthHandler(ready func(context.Context) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			w.WriteHeader(405)
			return
		}
		if r.URL.Path == "/health/live" {
			_, _ = w.Write([]byte("ok\n"))
			return
		}
		if r.URL.Path != "/health/ready" {
			http.NotFound(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if ready == nil || ready(ctx) != nil {
			w.WriteHeader(503)
			_, _ = w.Write([]byte("unavailable\n"))
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
}
