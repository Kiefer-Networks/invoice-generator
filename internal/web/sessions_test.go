package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kiefer-networks/invoice-generator/internal/auth"
	"github.com/kiefer-networks/invoice-generator/internal/store"
)

type sessionAuth struct {
	*fakeAuth
	sessions []auth.Session
	revoked  string
	all      bool
	event    store.AuditEvent
}

func (s *sessionAuth) ListSessions(context.Context, *http.Cookie) ([]auth.Session, error) {
	return s.sessions, nil
}
func (s *sessionAuth) RevokeSession(_ context.Context, _ *http.Cookie, id string, event store.AuditEvent) error {
	s.revoked = id
	s.event = event
	return nil
}
func (s *sessionAuth) RevokeAllSessions(_ context.Context, _ *http.Cookie, event store.AuditEvent) error {
	s.all = true
	s.event = event
	return nil
}

func TestSessionPageDoesNotExposeSecretsAndRevokesSpecificSession(t *testing.T) {
	id := "EREREREREREREREREREREQ"
	a := &sessionAuth{fakeAuth: &fakeAuth{}, sessions: []auth.Session{{ID: id, CreatedAt: time.Now(), AuthorizationExpiresAt: time.Now().Add(time.Minute)}}}
	h, err := New(Dependencies{Auth: a, Config: Config{AllowedHosts: []string{"app.example.test"}, BodyLimit: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	view := httptest.NewRequest(http.MethodGet, "https://app.example.test/settings/sessions", nil)
	view.Host = "app.example.test"
	view.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, view)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), id+"/revoke") {
		t.Fatalf("session page status=%d body=%q", w.Code, w.Body.String())
	}
	if strings.Contains(strings.ToLower(w.Body.String()), "token_hash") || strings.Contains(strings.ToLower(w.Body.String()), "csrf_secret") {
		t.Fatal("session page exposed a secret field")
	}
	request := httptest.NewRequest(http.MethodPost, "https://app.example.test/settings/sessions/"+id+"/revoke", strings.NewReader("csrf_token=csrf"))
	request.Host = "app.example.test"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "session"})
	w = httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != http.StatusSeeOther || a.revoked != id {
		t.Fatalf("revoke status=%d id=%q", w.Code, a.revoked)
	}
	if a.event.ActorSubject != "subject-ada" || a.event.RequestID == "" || a.event.Action != "session.revoked" {
		t.Fatalf("audit event=%#v", a.event)
	}
}

func TestDevelopmentCookieLookupUsesDedicatedName(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/", nil)
	r.AddCookie(&http.Cookie{Name: auth.DevelopmentSessionCookieName, Value: "development"})
	c := cookie(r, auth.SessionCookieName)
	if c == nil || c.Name != auth.DevelopmentSessionCookieName {
		t.Fatalf("cookie=%#v", c)
	}
}
