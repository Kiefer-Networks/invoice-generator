package web

import (
	"net/http"
	"strings"

	"github.com/kiefer-networks/invoice-generator/internal/auth"
)

func (a *app) sessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	manager, ok := a.auth.(sessionAdministrator)
	if !ok {
		http.Error(w, "session administration unavailable", http.StatusServiceUnavailable)
		return
	}
	items, err := manager.ListSessions(r.Context(), cookie(r, auth.SessionCookieName))
	if err != nil {
		http.Error(w, "unable to load sessions", http.StatusInternalServerError)
		return
	}
	a.renderTemplate(w, "sessionsPage", a.withPageData(r, pageData{Sessions: items}))
}

func (a *app) sessionRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	const prefix, suffix = "/settings/sessions/", "/revoke"
	if !strings.HasSuffix(r.URL.Path, suffix) {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
	if len(id) != 32 {
		http.NotFound(w, r)
		return
	}
	manager, ok := a.auth.(sessionAdministrator)
	if !ok {
		http.Error(w, "session administration unavailable", http.StatusServiceUnavailable)
		return
	}
	items, err := manager.ListSessions(r.Context(), cookie(r, auth.SessionCookieName))
	current := false
	if err == nil {
		for _, item := range items {
			if item.ID == id {
				current = item.Current
				break
			}
		}
	}
	if err != nil || manager.RevokeSession(r.Context(), cookie(r, auth.SessionCookieName), id) != nil {
		http.Error(w, "unable to revoke session", http.StatusBadRequest)
		return
	}
	if current {
		a.deleteSessionCookie(w)
		http.Redirect(w, r, "/auth/signed-out", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/settings/sessions", http.StatusSeeOther)
}

func (a *app) revokeAllSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	manager, ok := a.auth.(sessionAdministrator)
	if !ok {
		http.Error(w, "session administration unavailable", http.StatusServiceUnavailable)
		return
	}
	if err := manager.RevokeAllSessions(r.Context(), cookie(r, auth.SessionCookieName)); err != nil {
		http.Error(w, "unable to revoke sessions", http.StatusInternalServerError)
		return
	}
	a.deleteSessionCookie(w)
	http.Redirect(w, r, "/auth/signed-out", http.StatusSeeOther)
}

func (a *app) deleteSessionCookie(w http.ResponseWriter) {
	deleted := &http.Cookie{Name: auth.SessionCookieNameForSecure(!a.config.Development), Value: "", Path: "/", MaxAge: -1} // #nosec G124 -- protectCookie applies every response-only security attribute.
	a.protectCookie(deleted, http.SameSiteStrictMode)
	http.SetCookie(w, deleted)
}
