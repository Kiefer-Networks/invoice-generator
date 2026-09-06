package store

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"
)

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
