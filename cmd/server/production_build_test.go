package main

import (
	"bytes"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionBinaryExcludesDevelopment(t *testing.T) {
	cmd := exec.Command("go", "list", "-tags=production", "-deps", ".")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	for _, forbidden := range []string{"internal/devmode", "testdata/dev", "net/http/httptest"} {
		if strings.Contains(string(out), forbidden) {
			t.Fatalf("production links %s", forbidden)
		}
	}
	binary := filepath.Join(t.TempDir(), "server")
	cmd = exec.Command("go", "build", "-tags=production", "-trimpath", "-buildvcs=false", "-ldflags=-s -w -buildid=", "-o", binary, ".")
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
	if out, err = cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"Example Development GmbH", "local-fixture-only", "LOCAL DEVELOPMENT", "Development Pocket ID", "internal/devmode", "testdata/dev"} {
		if bytes.Contains(data, []byte(forbidden)) {
			t.Fatalf("binary contains %s", forbidden)
		}
	}
	f, err := elf.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, s := range f.Sections {
		if strings.HasPrefix(s.Name, ".debug") || s.Name == ".symtab" {
			t.Fatalf("debug symbols: %s", s.Name)
		}
	}
}
