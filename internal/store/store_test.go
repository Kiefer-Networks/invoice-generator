package store

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestOpenAppliesSecurityPragmas(t *testing.T) {
	t.Parallel()

	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	assertSecurityPragmas(t, s)
}

func TestOpenEscapesReservedPathCharacters(t *testing.T) {
	t.Parallel()

	name := "app?#reserved.db"
	if runtime.GOOS == "windows" {
		// Windows reserves '?' in file names, so '#' is the supported
		// filesystem-backed URI delimiter regression case on this platform.
		name = "app#reserved.db"
	}
	path := filepath.Join(t.TempDir(), name)
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("intended database path was not created: %v", err)
	}
	assertSecurityPragmas(t, s)
}

func assertSecurityPragmas(t *testing.T, s *Store) {
	t.Helper()

	checks := map[string]string{
		"foreign_keys":   "1",
		"journal_mode":   "wal",
		"busy_timeout":   "5000",
		"trusted_schema": "0",
	}
	for pragma, want := range checks {
		var got string
		if err := s.db.QueryRowContext(context.Background(), "PRAGMA "+pragma).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if strings.ToLower(got) != want {
			t.Fatalf("%s=%q, want %q", pragma, got, want)
		}
	}
}
