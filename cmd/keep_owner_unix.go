//go:build !windows

package cmd

import (
	"errors"
	"os"
	"syscall"
)

// chownFile is a seam for tests.
var chownFile = func(f *os.File, uid, gid int) error { return f.Chown(uid, gid) }

// keepOwner gives the temp file the replaced file's owner, so a run under
// sudo does not leave the config owned by root. Without root the chown
// fails with EPERM, and the owner is then already the caller.
func keepOwner(tmp *os.File, existing os.FileInfo) error {
	st, ok := existing.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if err := chownFile(tmp, int(st.Uid), int(st.Gid)); err != nil && !errors.Is(err, syscall.EPERM) {
		return err
	}
	return nil
}
