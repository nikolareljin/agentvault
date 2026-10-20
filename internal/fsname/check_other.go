//go:build !windows

package fsname

import "os"

// Check is a no-op off Windows: there a name opens only itself.
func Check(root, p string) error { return nil }

// RenameNoReplace is for Windows writers; elsewhere it refuses an existing
// dst and renames (no aliases exist, so the check is only best effort).
func RenameNoReplace(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: os.ErrExist}
	}
	return os.Rename(src, dst)
}
