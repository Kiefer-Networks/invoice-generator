//go:build windows

package documents

import "os"

// Windows has no POSIX directory-fsync API. FlushFileBuffers on the file's
// reopened, writable handle flushes its post-rename metadata. Go File.Sync uses
// FlushFileBuffers on Windows. Keep os.Root-relative operations for containment;
// do not reconstruct absolute paths for an uncontained MoveFileEx call.
// https://learn.microsoft.com/en-us/windows/win32/fileio/file-caching
// https://learn.microsoft.com/en-us/windows/win32/api/fileapi/nf-fileapi-flushfilebuffers
// An unsupported filesystem/flush operation fails publication; never swallow it.
func syncPublication(root *os.Root, key string) error {
	file, err := root.OpenFile(key, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}
