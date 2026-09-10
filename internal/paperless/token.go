package paperless

import (
	"errors"
	"io"
	"os"
	"runtime"
	"strings"
)

// ReadToken reloads mounted tokens on every attempt. Errors never contain paths
// or contents. POSIX requires owner-only permissions; Windows uses mounted ACLs.
func ReadToken(path string) (string, error) {
	if path == "" {
		return "", errors.New("configuration_missing")
	}
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return "", errors.New("configuration_missing")
	}
	invalid := errors.New("configuration_invalid")
	if e != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > 4096 || info.Size() == 0 {
		return "", invalid
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return "", invalid
	}
	f, e := os.Open(path) // #nosec G304 -- Administrator-configured token mount; protected regular-file type and opened identity are checked.
	if e != nil {
		return "", invalid
	}
	defer func() { _ = f.Close() }() // Read-only credential file; read errors are checked below.
	opened, e := f.Stat()
	if e != nil || !os.SameFile(info, opened) {
		return "", invalid
	}
	data, e := io.ReadAll(io.LimitReader(f, 4097))
	if e != nil || len(data) > 4096 {
		return "", invalid
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.ContainsAny(token, "\r\n\x00") {
		return "", invalid
	}
	return token, nil
}
