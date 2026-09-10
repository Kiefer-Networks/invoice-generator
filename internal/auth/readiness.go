package auth

import (
	"context"
	"errors"
	"net/url"
)

// CheckDiscovery fetches and validates fresh provider metadata without changing
// the active login provider. Endpoint drift requires a deliberate restart.
func (m *Manager) CheckDiscovery(ctx context.Context) error {
	p, ok := m.provider.(discoveredProvider)
	if !ok {
		return errors.New("discovery unavailable")
	}
	issuer, err := url.Parse(m.issuer)
	if err != nil {
		return err
	}
	if _, err := validateRedirect(m.redirectURL, !m.secureCookies); err != nil {
		return err
	}
	doc, err := discover(ctx, p.client, issuer)
	if err != nil {
		return err
	}
	if doc.AuthorizationEndpoint != p.oauth.Endpoint.AuthURL || doc.TokenEndpoint != p.oauth.Endpoint.TokenURL {
		return errors.New("provider metadata changed")
	}
	return nil
}
