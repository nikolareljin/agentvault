//go:build windows

package memory

import (
	"fmt"
	"os"
)

// checkTokenMode on Windows checks only that the path is a regular file:
// POSIX mode bits do not exist there, and the file is created under the
// user's profile, which other accounts cannot read by default.
func checkTokenMode(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("memory: %s is not a regular file", path)
	}
	return nil
}
