//go:build windows

package gitid

// ownedByCurrentUser fails closed on Windows: without an owner SID check,
// git is not run and root commits are not read.
func ownedByCurrentUser(string) bool { return false }
