package cipolicy

import (
	"os"
	"strings"
	"testing"
)

func TestRuntimeAPKLocks(t *testing.T) {
	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	installer, err := os.ReadFile("../../docker/install-locked-apks")
	if err != nil {
		t.Fatal(err)
	}
	amd64, err := os.ReadFile("../../docker/apk-lock.amd64")
	if err != nil {
		t.Fatal(err)
	}
	arm64, err := os.ReadFile("../../docker/apk-lock.arm64")
	if err != nil {
		t.Fatal(err)
	}
	if err := RuntimeAPKLocks(dockerfile, installer, amd64, arm64); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeAPKInstallerPreservesExactBasePackages(t *testing.T) {
	installer, err := os.ReadFile("../../docker/install-locked-apks")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`/lib/apk/db/installed`,
		`grep -Eqv '^[A-Za-z0-9][A-Za-z0-9+_.-]*=[A-Za-z0-9][A-Za-z0-9+_.:~-]*$' "$lock"`,
		`grep -Fqx "$entry" "$installed"`,
		`apk add --no-cache --no-progress -- $(cat "$missing")`,
	} {
		if !strings.Contains(string(installer), required) {
			t.Errorf("installer does not safely preserve exact base packages: missing %q", required)
		}
	}
}

func TestRuntimeAPKLockRejectsBypassAndDrift(t *testing.T) {
	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	installer, err := os.ReadFile("../../docker/install-locked-apks")
	if err != nil {
		t.Fatal(err)
	}
	amd64, err := os.ReadFile("../../docker/apk-lock.amd64")
	if err != nil {
		t.Fatal(err)
	}
	arm64, err := os.ReadFile("../../docker/apk-lock.arm64")
	if err != nil {
		t.Fatal(err)
	}

	mutations := []struct {
		name                                string
		dockerfile, installer, amd64, arm64 []byte
	}{
		{"unlocked-install", []byte(strings.Replace(string(dockerfile), "RUN /usr/local/bin/install-locked-apks \"$TARGETARCH\"", "RUN apk add --no-cache chromium", 1)), installer, amd64, arm64},
		{"unpinned-base", []byte(strings.Replace(string(dockerfile), "FROM alpine:3.24.1@sha256:", "FROM alpine:3.24.1 # sha256:", 1)), installer, amd64, arm64},
		{"ignored-verification", dockerfile, []byte(strings.Replace(string(installer), `cmp -s "$expected" "$actual"`, `cmp -s "$expected" "$actual" || true`, 1)), amd64, arm64},
		{"missing-architecture", dockerfile, []byte(strings.Replace(string(installer), `arm64) apk_arch=aarch64; lock=/usr/local/share/apk-lock.arm64 ;;`, `arm64) apk_arch=aarch64; lock=/usr/local/share/apk-lock.amd64 ;;`, 1)), amd64, arm64},
		{"missing-package", dockerfile, installer, []byte(strings.Replace(string(amd64), "busybox=", "removed=", 1)), arm64},
		{"transitive-version-drift", dockerfile, installer, []byte(strings.Replace(string(amd64), "libpng=1.6.58-r1", "libpng=1.6.58-r0", 1)), arm64},
		{"floating-version", dockerfile, installer, []byte(strings.Replace(string(amd64), "ca-certificates=", "ca-certificates>=", 1)), arm64},
		{"duplicate-package", dockerfile, installer, append(append([]byte{}, amd64...), []byte("\nzlib=1.3.1-r2\n")...), arm64},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			if err := RuntimeAPKLocks(tc.dockerfile, tc.installer, tc.amd64, tc.arm64); err == nil {
				t.Fatal("unsafe runtime package lock accepted")
			}
		})
	}
}
