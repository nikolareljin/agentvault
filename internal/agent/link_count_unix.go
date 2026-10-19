//go:build !windows

package agent

import (
	"os"
	"syscall"
)

// HardLinkCount is how many names the file has. A file with more than one
// can be a credential under another name, which a check by name cannot see.
func HardLinkCount(_ string, info os.FileInfo) uint64 {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}
