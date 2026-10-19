//go:build !windows

package agent

import (
	"os"
	"syscall"
)

// HardLinkCount is how many names the file has. A file with more than one
// can be a credential under another name, which a check by name cannot see.
// When the count cannot be read the file is treated as linked: fail closed.
func HardLinkCount(_ string, info os.FileInfo) uint64 {
	if info != nil {
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			return uint64(st.Nlink)
		}
	}
	return 2
}
