//go:build !windows

package gitid

import (
	"os"
	"testing"
)

func TestOwnedByCurrentUser(t *testing.T) {
	if !ownedByCurrentUser(t.TempDir()) {
		t.Fatal("own temp dir reported as foreign")
	}
	if os.Getuid() != 0 && ownedByCurrentUser("/") {
		t.Fatal("/ reported as owned by a non-root user")
	}
	if ownedByCurrentUser("/does/not/exist") {
		t.Fatal("missing dir reported as owned")
	}
}
