package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestCreateSessionLimitedEnforcesMaximumAtomically(t *testing.T) {
	t.Parallel()
	s := openMigratedStore(t)
	repo := s.AuthRepository()
	ctx := context.Background()
	user, err := repo.UpsertUser(ctx, OIDCUser{Issuer: "https://issuer.example.test", Subject: "limited"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	const attempts, maximum = 12, 3
	var wg sync.WaitGroup
	errs := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			h := sha256.Sum256([]byte(string(rune('a' + i))))
			errs <- repo.CreateSessionLimited(ctx, StoredSession{ID: fmt.Sprintf("session-%d", i), UserID: user.ID, TokenHash: h[:], CSRFSecretHash: h[:], AuthorizationExpiresAt: now.Add(time.Minute), ExpiresAt: now.Add(time.Minute)}, now, maximum)
		}(i)
	}
	wg.Wait()
	close(errs)
	accepted := 0
	for err := range errs {
		if err == nil {
			accepted++
		} else if !errors.Is(err, ErrSessionLimit) {
			t.Fatal(err)
		}
	}
	if accepted != maximum {
		t.Fatalf("accepted %d sessions, want %d", accepted, maximum)
	}
}

func TestAuditedSessionRevocationIsScopedAndAtomic(t *testing.T) {
	t.Parallel()
	s := openMigratedStore(t)
	repo := s.AuthRepository()
	ctx := context.Background()
	first, err := repo.UpsertUser(ctx, OIDCUser{Issuer: "https://issuer.example.test", Subject: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.UpsertUser(ctx, OIDCUser{Issuer: "https://issuer.example.test", Subject: "second"})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Minute)
	for _, item := range []struct{ id, user string }{{"first-a", first.ID}, {"first-b", first.ID}, {"second-a", second.ID}} {
		h := sha256.Sum256([]byte(item.id))
		if err := repo.CreateSession(ctx, StoredSession{ID: item.id, UserID: item.user, TokenHash: h[:], CSRFSecretHash: h[:], AuthorizationExpiresAt: expires, ExpiresAt: expires}); err != nil {
			t.Fatal(err)
		}
	}
	event := AuditEvent{ActorSubject: "first", Action: "session.revoked_all", TargetType: "session", RequestID: "request-session"}
	if err := repo.DeleteSessionsForUserAudited(ctx, first.ID, event); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := s.DB().QueryRow(`SELECT count(*) FROM sessions WHERE user_id=?`, second.ID).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("other user sessions=%d err=%v", remaining, err)
	}
	var actor, request string
	if err := s.DB().QueryRow(`SELECT actor_subject,request_id FROM audit_events WHERE action='session.revoked_all'`).Scan(&actor, &request); err != nil || actor != "first" || request != "request-session" {
		t.Fatalf("audit actor=%q request=%q err=%v", actor, request, err)
	}

	h := sha256.Sum256([]byte("rollback"))
	if err := repo.CreateSession(ctx, StoredSession{ID: "rollback", UserID: second.ID, TokenHash: h[:], CSRFSecretHash: h[:], AuthorizationExpiresAt: expires, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}
	rejectAuditInserts(t, s)
	if err := repo.DeleteSessionForUserAudited(ctx, "rollback", second.ID, AuditEvent{ActorSubject: "second", Action: "session.revoked", TargetType: "session", RequestID: "request-fail"}); !errors.Is(err, ErrAudit) {
		t.Fatalf("error=%v, want ErrAudit", err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM sessions WHERE id='rollback'`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("revocation survived audit failure count=%d err=%v", remaining, err)
	}
	if err := repo.DeleteSessionsForUserAudited(ctx, second.ID, AuditEvent{ActorSubject: "second", Action: "session.revoked_all", TargetType: "session", RequestID: "request-fail-all"}); !errors.Is(err, ErrAudit) {
		t.Fatalf("bulk error=%v, want ErrAudit", err)
	}
	if err := s.DB().QueryRow(`SELECT count(*) FROM sessions WHERE user_id=?`, second.ID).Scan(&remaining); err != nil || remaining != 2 {
		t.Fatalf("bulk revocation survived audit failure count=%d err=%v", remaining, err)
	}
}

func TestAuthRepositoryKeepsIssuerSubjectIdentityWhenEmailChanges(t *testing.T) {
	t.Parallel()
	s := openMigratedStore(t)
	repo := s.AuthRepository()
	ctx := context.Background()

	first, err := repo.UpsertUser(ctx, OIDCUser{
		Issuer: "https://pocket-id.example.test", Subject: "person-1", Email: "old@example.test", DisplayName: "Person",
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.UpsertUser(ctx, OIDCUser{
		Issuer: "https://pocket-id.example.test", Subject: "person-1", Email: "new@example.test", DisplayName: "Person Two",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("email change created a different identity: %q != %q", second.ID, first.ID)
	}
	if second.Email != "new@example.test" {
		t.Fatalf("email=%q, want updated email", second.Email)
	}
}

func TestAuthRepositoryStoresOnlySessionHash(t *testing.T) {
	t.Parallel()
	s := openMigratedStore(t)
	repo := s.AuthRepository()
	ctx := context.Background()
	user, err := repo.UpsertUser(ctx, OIDCUser{Issuer: "https://pocket-id.example.test", Subject: "person-1"})
	if err != nil {
		t.Fatal(err)
	}
	tokenHash := sha256.Sum256([]byte("derived token hash"))
	csrfHash := sha256.Sum256([]byte("derived csrf hash"))
	expires := time.Now().UTC().Add(15 * time.Minute).Truncate(time.Millisecond)
	if err := repo.CreateSession(ctx, StoredSession{ID: "session-1", UserID: user.ID, TokenHash: tokenHash[:], CSRFSecretHash: csrfHash[:], AuthorizationExpiresAt: expires, ExpiresAt: expires}); err != nil {
		t.Fatal(err)
	}

	var storedTokenHash []byte
	if err := s.DB().QueryRowContext(ctx, "SELECT token_hash FROM sessions WHERE id = 'session-1'").Scan(&storedTokenHash); err != nil {
		t.Fatal(err)
	}
	if string(storedTokenHash) != string(tokenHash[:]) {
		t.Fatal("stored session token hash differs from supplied keyed hash")
	}
	var sessionCount int
	if err := s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE token_hash = ?", []byte("raw session token")).Scan(&sessionCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 0 {
		t.Fatal("raw session token was stored")
	}
}

func TestAuthRepositoryCleanupOrdersSameSecondFractionalExpiry(t *testing.T) {
	t.Parallel()
	s := openMigratedStore(t)
	repo := s.AuthRepository()
	ctx := context.Background()
	user, err := repo.UpsertUser(ctx, OIDCUser{Issuer: "https://issuer.example.test", Subject: "subject"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, item := range []struct {
		id     string
		expiry time.Time
	}{{"expired", base.Add(100 * time.Millisecond)}, {"active", base.Add(110 * time.Millisecond)}} {
		hash := sha256.Sum256([]byte(item.id))
		if err := repo.CreateSession(ctx, StoredSession{ID: item.id, UserID: user.ID, TokenHash: hash[:], CSRFSecretHash: hash[:], AuthorizationExpiresAt: item.expiry, ExpiresAt: item.expiry}); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.DeleteExpiredSessions(ctx, base.Add(105*time.Millisecond), 10); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id='expired'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("cleanup retained .100 session before .105 cutoff")
	}
	if err := s.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE id='active'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("cleanup deleted .110 session after .105 cutoff")
	}
}
