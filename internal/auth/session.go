package auth

const (
	SessionCookieName            = "__Host-invoice_session"
	DevelopmentSessionCookieName = "invoice_session_dev"
)

const sessionCookieName = SessionCookieName

func SessionCookieNameForSecure(secure bool) string {
	if secure {
		return SessionCookieName
	}
	return DevelopmentSessionCookieName
}
