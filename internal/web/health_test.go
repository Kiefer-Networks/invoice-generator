package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	for _, failed := range []string{"", "database", "migrations", "discovery", "callback", "chrome", "documents"} {
		t.Run(failed, func(t *testing.T) {
			calls := 0
			h := HealthHandler(func(context.Context) error {
				calls++
				if failed != "" {
					return errors.New("secret /private/path version 123: " + failed)
				}
				return nil
			})
			for _, path := range []string{"/health/live", "/health/ready"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
				want, body := 200, "ok\n"
				if path == "/health/ready" && failed != "" {
					want, body = 503, "unavailable\n"
				}
				if w.Code != want || w.Body.String() != body {
					t.Fatalf("%s: %d %q", path, w.Code, w.Body.String())
				}
				if path == "/health/live" && calls != 0 {
					t.Fatal("liveness checked dependencies")
				}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/health/ready", nil))
			if w.Code != 405 {
				t.Fatal(w.Code)
			}
		})
	}
}
