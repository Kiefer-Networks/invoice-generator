package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

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
