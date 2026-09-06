package paperless

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPaperlessTokenFileSafeAndReloadable(t *testing.T) {
	p := filepath.Join(t.TempDir(), "token")
	if _, e := ReadToken(p); e == nil || e.Error() != "configuration_missing" {
		t.Fatal(e)
	}
	for _, value := range []string{"one", "two\n"} {
		if e := os.WriteFile(p, []byte(value), 0600); e != nil {
			t.Fatal(e)
		}
		got, e := ReadToken(p)
		if e != nil || got != strings.TrimSpace(value) {
			t.Fatal(got, e)
		}
	}
	for _, value := range []string{"", strings.Repeat("s", 4097), "secret\r\nother"} {
		os.WriteFile(p, []byte(value), 0600)
		if _, e := ReadToken(p); e == nil || strings.Contains(e.Error(), "secret") {
			t.Fatal(e)
		}
	}
	if runtime.GOOS != "windows" {
		os.WriteFile(p, []byte("secret"), 0600)
		os.Chmod(p, 0644)
		if _, e := ReadToken(p); e == nil {
			t.Fatal("permissions")
		}
	}
}

func TestPaperlessConfigParseDoesNotLeakToken(t *testing.T) {
	p := writeTemp(t, t.TempDir(), "paperless.yaml", "url: https://paperless.internal\napi_key: tok\ntags: tok\n")
	if _, e := Load(p); e == nil || strings.Contains(e.Error(), "tok") {
		t.Fatal(e)
	}
}

func TestPaperlessConfigRequiresProtectedRegularFile(t *testing.T) {
	if _, e := Load(t.TempDir()); e == nil {
		t.Fatal("directory accepted")
	}
	if runtime.GOOS != "windows" {
		p := writeTemp(t, t.TempDir(), "paperless.yaml", "url: https://paperless.internal\napi_key: tok\n")
		if e := os.Chmod(p, 0644); e != nil {
			t.Fatal(e)
		}
		if _, e := Load(p); e == nil {
			t.Fatal("broad permissions accepted")
		}
	}
}
