package web

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kiefer-networks/invoice-generator/internal/store"
)

func TestAuditMutationTreatsAtomicAuditFailureAsInternalError(t *testing.T) {
	database, err := store.Open(context.Background(), t.TempDir()+"/audit.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = database.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	application := &app{store: database, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	request := httptest.NewRequest(http.MethodPost, "https://app.example.test/customers/new", nil)
	recorder := httptest.NewRecorder()

	if application.auditMutation(recorder, request, "customer.created", "customer", "", fmt.Errorf("write: %w", store.ErrAudit)) {
		t.Fatal("auditMutation() = true, want request stopped")
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	var count int
	if err = database.DB().QueryRow(`SELECT count(*) FROM audit_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("audit count = %d, error = %v; want no misleading failure event", count, err)
	}
}

func TestCustomerHandlerRollsBackMutationWhenAuditStorageFails(t *testing.T) {
	handler, database := auditTestApp(t, &fakeAuth{}, Config{})
	if _, err := database.DB().Exec(`CREATE TRIGGER reject_audit_insert BEFORE INSERT ON audit_events BEGIN SELECT RAISE(FAIL, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	form := url.Values{
		"number":             {"C-ROLLBACK"},
		"display_name":       {"Rollback Buyer"},
		"country":            {"DE"},
		"preferred_language": {"de"},
		"currency":           {"EUR"},
		"payment_terms_days": {"14"},
		"csrf_token":         {"csrf"},
	}
	request := httptest.NewRequest(http.MethodPost, "https://app.example.test/customers/new", strings.NewReader(form.Encode()))
	request.Host = "app.example.test"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "__Host-invoice_session", Value: "session"}) // #nosec G124 -- Request-only fixture.
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d; body=%q", recorder.Code, http.StatusInternalServerError, recorder.Body.String())
	}
	var count int
	if err := database.DB().QueryRow(`SELECT count(*) FROM customers WHERE number='C-ROLLBACK'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("customer count = %d, want rollback", count)
	}
}
