//go:build !windows

package cmd

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// Replacing an existing file chowns the temp file to the old owner.
func TestWriteFileAtomicKeepsOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	orig := chownFile
	t.Cleanup(func() { chownFile = orig })
	calls := 0
	chownFile = func(f *os.File, uid, gid int) error {
		calls++
		if uid != int(st.Uid) || gid != int(st.Gid) {
			t.Errorf("chown to %d:%d, want %d:%d", uid, gid, st.Uid, st.Gid)
		}
		return orig(f, uid, gid)
	}
	if err := writeFileAtomicKeepMode(path, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("chown called %d times, want 1", calls)
	}
}

// Without root a chown to another owner fails with EPERM; that is not an error.
func TestKeepOwnerIgnoresEPERM(t *testing.T) {
	orig := chownFile
	t.Cleanup(func() { chownFile = orig })
	chownFile = func(*os.File, int, int) error { return &os.PathError{Op: "chown", Err: syscall.EPERM} }
	f, err := os.CreateTemp(t.TempDir(), "x")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	fi, _ := f.Stat()
	if err := keepOwner(f, fi); err != nil {
		t.Fatal(err)
	}
}
