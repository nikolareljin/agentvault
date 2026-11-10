//go:build !windows

package memory

import (
	"fmt"
	"os"
)

// checkTokenMode refuses a token file that is group or world accessible:
// the token is the only credential, and a 0644 file is a published one.
func checkTokenMode(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("memory: %s is not a regular file", path)
	}
	if mode := info.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("memory: %s is mode %04o; chmod 600 it (it must be readable by you alone)", path, mode)
	}
	return nil
}
