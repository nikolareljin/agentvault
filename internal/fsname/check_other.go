//go:build !windows

package fsname

// Check is a no-op off Windows: there a name opens only itself.
func Check(root, p string) error { return nil }
