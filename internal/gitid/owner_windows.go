//go:build windows

package gitid

// ownedByCurrentUser on Windows defers to git's own safe.directory owner
// check, which refuses a repository owned by another user.
func ownedByCurrentUser(string) bool { return true }
