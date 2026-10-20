//go:build windows

package gitid

import (
	"context"
	"testing"
)

func TestWindowsOwnerCheckFailsClosed(t *testing.T) {
	if ownedByCurrentUser(t.TempDir()) {
		t.Fatal("Windows owner check must fail closed")
	}
	dir, _ := newRepo(t, "")
	ownerCheck = ownedByCurrentUser
	r, err := Identify(context.Background(), dir, nil)
	if err != nil || len(r.RootCommits) != 0 || r.RootSkipped == "" {
		t.Fatalf("got %+v %v", r, err)
	}
}
