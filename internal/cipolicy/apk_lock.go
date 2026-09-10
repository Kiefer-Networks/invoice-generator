package cipolicy

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var apkLockEntry = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9+_.-]*)=([A-Za-z0-9][A-Za-z0-9+_.:~-]*)$`)

// RuntimeAPKLocks ensures the production image installs the complete, exact
// package set selected for each supported architecture and verifies the result.
func RuntimeAPKLocks(dockerfile, installer, amd64, arm64 []byte) error {
	for arch, lock := range map[string]struct {
		data []byte
		hash string
	}{
		"amd64": {amd64, "9321c96b5c1f5389978e56340c541f35d90c717ad14c8a19c15d744d09cc6acd"},
		"arm64": {arm64, "16b5178be9cd076d1ca6cd64ae00dec33c35d7c953d7f78c7b2417ce360c61a2"},
	} {
		if actual := fmt.Sprintf("%x", sha256.Sum256(lock.data)); actual != lock.hash {
			return fmt.Errorf("%s runtime package lock differs from its reviewed digest", arch)
		}
	}
	_, err := validateAPKLock("amd64", amd64)
	if err != nil {
		return err
	}
	_, err = validateAPKLock("arm64", arm64)
	if err != nil {
		return err
	}
	d := string(dockerfile)
	for _, required := range []string{
		"ARG TARGETARCH",
		"COPY --chmod=0555 docker/install-locked-apks /usr/local/bin/install-locked-apks",
		"COPY docker/apk-lock.amd64 docker/apk-lock.arm64 /usr/local/share/",
		`RUN /usr/local/bin/install-locked-apks "$TARGETARCH"`,
	} {
		if !strings.Contains(d, required) {
			return fmt.Errorf("runtime package lock integration missing: %s", required)
		}
	}
	start := strings.Index(d, "FROM alpine:3.24.1@sha256:")
	if start < 0 {
		return fmt.Errorf("digest-pinned Alpine 3.24.1 runtime stage missing")
	}
	runtime := d[start:]
	if end := strings.Index(runtime, "\nFROM runtime AS development"); end >= 0 {
		runtime = runtime[:end]
	}
	if strings.Contains(runtime, "apk add") {
		return fmt.Errorf("runtime stage contains an unlocked package install")
	}

	s := string(installer)
	for _, required := range []string{
		"set -eu",
		"amd64) apk_arch=x86_64; lock=/usr/local/share/apk-lock.amd64 ;;",
		"arm64) apk_arch=aarch64; lock=/usr/local/share/apk-lock.arm64 ;;",
		`test "$(apk --print-arch)" = "$apk_arch"`,
		"https://dl-cdn.alpinelinux.org/alpine/v3.24/main",
		"https://dl-cdn.alpinelinux.org/alpine/v3.24/community",
		`/lib/apk/db/installed`,
		`grep -Eqv '^[A-Za-z0-9][A-Za-z0-9+_.-]*=[A-Za-z0-9][A-Za-z0-9+_.:~-]*$' "$lock"`,
		`grep -Fqx "$entry" "$installed"`,
		`apk add --no-cache --no-progress -- $(cat "$missing")`,
		`cmp -s "$expected" "$actual"`,
	} {
		if !strings.Contains(s, required) {
			return fmt.Errorf("runtime package installer guard missing: %s", required)
		}
	}
	for _, forbidden := range []string{"--allow-untrusted", "|| true", "http://dl-cdn"} {
		if strings.Contains(s, forbidden) {
			return fmt.Errorf("runtime package installer contains forbidden bypass")
		}
	}
	return nil
}

func validateAPKLock(arch string, data []byte) ([]string, error) {
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(string(data), "\r\n", "\n")), "\n")
	if len(lines) < 100 {
		return nil, fmt.Errorf("%s runtime package lock is incomplete", arch)
	}
	names := make([]string, 0, len(lines))
	seen := make(map[string]bool, len(lines))
	for _, line := range lines {
		match := apkLockEntry.FindStringSubmatch(line)
		if match == nil {
			return nil, fmt.Errorf("%s runtime package lock has invalid entry %q", arch, line)
		}
		if seen[match[1]] {
			return nil, fmt.Errorf("%s runtime package lock repeats %s", arch, match[1])
		}
		seen[match[1]] = true
		names = append(names, match[1])
	}
	if !sort.StringsAreSorted(lines) {
		return nil, fmt.Errorf("%s runtime package lock is not sorted", arch)
	}
	for _, required := range []string{"alpine-baselayout", "alpine-release", "apk-tools", "busybox", "ca-certificates", "chromium", "curl", "font-liberation", "libcrypto3", "libssl3", "openjdk21-jdk"} {
		if !seen[required] {
			return nil, fmt.Errorf("%s runtime package lock misses %s", arch, required)
		}
	}
	return names, nil
}
