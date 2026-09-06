package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// AuthRepository persists Pocket ID identities and local, hashed sessions.
type AuthRepository struct{ store *Store }

// AuthRepository returns the authentication repository backed by this store.
func (s *Store) AuthRepository() *AuthRepository { return &AuthRepository{store: s} }

type OIDCUser struct {
	ID, Issuer, Subject, DisplayName, Email string
	LastLoginAt, LastAuthorizationAt        time.Time
}

type StoredSession struct {
	ID                                string
	UserID                            string
	TokenHash, CSRFSecretHash         []byte
	AuthorizationExpiresAt, ExpiresAt time.Time
}

func (r *AuthRepository) UpsertUser(ctx context.Context, user OIDCUser) (OIDCUser, error) {
	if user.Issuer == "" || user.Subject == "" {
		return OIDCUser{}, errors.New("issuer and subject are required")
	}
	if user.ID == "" {
		var err error
		user.ID, err = newAuthID()
		if err != nil {
			return OIDCUser{}, err
		}
	}
	now := time.Now().UTC()
	if user.LastLoginAt.IsZero() {
		user.LastLoginAt = now
	}
	if user.LastAuthorizationAt.IsZero() {
		user.LastAuthorizationAt = now
	}
	_, err := r.store.db.ExecContext(ctx, `
INSERT INTO oidc_users (id, issuer, subject, display_name, email, last_login_at, last_authorization_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(issuer, subject) DO UPDATE SET
 display_name=excluded.display_name, email=excluded.email,
 last_login_at=excluded.last_login_at, last_authorization_at=excluded.last_authorization_at, active=1,
 updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now')`,
		user.ID, user.Issuer, user.Subject, user.DisplayName, user.Email, formatAuthTime(user.LastLoginAt), formatAuthTime(user.LastAuthorizationAt))
	if err != nil {
		return OIDCUser{}, fmt.Errorf("upsert oidc user: %w", err)
	}
	return r.UserByIssuerSubject(ctx, user.Issuer, user.Subject)
}

func (r *AuthRepository) UserByIssuerSubject(ctx context.Context, issuer, subject string) (OIDCUser, error) {
	var user OIDCUser
	var login, authorization sql.NullString
	err := r.store.db.QueryRowContext(ctx, `SELECT id, issuer, subject, display_name, email, last_login_at, last_authorization_at FROM oidc_users WHERE issuer=? AND subject=? AND active=1`, issuer, subject).Scan(&user.ID, &user.Issuer, &user.Subject, &user.DisplayName, &user.Email, &login, &authorization)
	if err != nil {
		return OIDCUser{}, err
	}
	user.LastLoginAt = parseAuthTime(login.String)
	user.LastAuthorizationAt = parseAuthTime(authorization.String)
	return user, nil
}

func (r *AuthRepository) CreateSession(ctx context.Context, session StoredSession) error {
	if session.ID == "" || session.UserID == "" || len(session.TokenHash) == 0 || len(session.CSRFSecretHash) == 0 || session.ExpiresAt.IsZero() || session.AuthorizationExpiresAt.IsZero() {
		return errors.New("incomplete session")
	}
	_, err := r.store.db.ExecContext(ctx, `INSERT INTO sessions (id,user_id,token_hash,csrf_secret_hash,authorization_expires_at,expires_at) VALUES (?,?,?,?,?,?)`, session.ID, session.UserID, session.TokenHash, session.CSRFSecretHash, formatSessionTime(session.AuthorizationExpiresAt), formatSessionTime(session.ExpiresAt))
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

func (r *AuthRepository) SessionByTokenHash(ctx context.Context, tokenHash []byte, now time.Time) (StoredSession, OIDCUser, error) {
	var session StoredSession
	var user OIDCUser
	var authExpiry, expiry string
	err := r.store.db.QueryRowContext(ctx, `
SELECT s.id,s.user_id,s.token_hash,s.csrf_secret_hash,s.authorization_expires_at,s.expires_at,
 u.id,u.issuer,u.subject,u.display_name,u.email
FROM sessions s JOIN oidc_users u ON u.id=s.user_id
WHERE s.token_hash=? AND CAST(s.expires_at AS INTEGER)>? AND CAST(s.authorization_expires_at AS INTEGER)>? AND u.active=1`, tokenHash, now.UTC().UnixNano(), now.UTC().UnixNano()).Scan(&session.ID, &session.UserID, &session.TokenHash, &session.CSRFSecretHash, &authExpiry, &expiry, &user.ID, &user.Issuer, &user.Subject, &user.DisplayName, &user.Email)
	if err != nil {
		return StoredSession{}, OIDCUser{}, err
	}
	session.AuthorizationExpiresAt = parseSessionTime(authExpiry)
	session.ExpiresAt = parseSessionTime(expiry)
	return session, user, nil
}

func (r *AuthRepository) DeleteSessionByTokenHash(ctx context.Context, tokenHash []byte) error {
	_, err := r.store.db.ExecContext(ctx, "DELETE FROM sessions WHERE token_hash=?", tokenHash)
	return err
}
func (r *AuthRepository) DeleteSessionsForUser(ctx context.Context, userID string) error {
	_, err := r.store.db.ExecContext(ctx, "DELETE FROM sessions WHERE user_id=?", userID)
	return err
}
func (r *AuthRepository) DeleteExpiredSessions(ctx context.Context, now time.Time, limit int) error {
	if limit <= 0 {
		return nil
	}
	_, err := r.store.db.ExecContext(ctx, `DELETE FROM sessions WHERE id IN (SELECT id FROM sessions WHERE CAST(expires_at AS INTEGER) <= ? OR CAST(authorization_expires_at AS INTEGER) <= ? LIMIT ?)`, now.UTC().UnixNano(), now.UTC().UnixNano(), limit)
	return err
}

func newAuthID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate identifier: %w", err)
	}
	return hex.EncodeToString(b), nil
}
func formatAuthTime(t time.Time) string    { return t.UTC().Format(time.RFC3339Nano) }
func parseAuthTime(value string) time.Time { t, _ := time.Parse(time.RFC3339Nano, value); return t }
func formatSessionTime(t time.Time) string { return fmt.Sprintf("%019d", t.UTC().UnixNano()) }
func parseSessionTime(value string) time.Time {
	n, _ := strconv.ParseInt(value, 10, 64)
	return time.Unix(0, n).UTC()
}
