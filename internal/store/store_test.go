package store

import (
	"context"
	"path/filepath"
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
