//go:build !windows

package documents

import "os"

// syncPublication makes the renamed directory entry durable before SQLite may
// publish it. File.Sync before rename alone does not persist the directory entry.
func syncPublication(root *os.Root, _ string) error {
	dir, err := root.Open(".")
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
