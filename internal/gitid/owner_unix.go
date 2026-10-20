//go:build !windows

package gitid

import (
	"os"
	"syscall"
)

// ownedByCurrentUser reports whether dir belongs to this process's user, so
// git is never run against a repository another user could have planted.
func ownedByCurrentUser(dir string) bool {
	info, err := os.Stat(dir) // #nosec G703 -- read-only access to the repository the caller named, as git itself reads it
	if err != nil {
		return false
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}
