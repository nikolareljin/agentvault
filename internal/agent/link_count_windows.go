//go:build windows

package agent

import (
	"os"
	"syscall"
)

// HardLinkCount is how many names the file has. A file with more than one
// can be a credential under another name, which a check by name cannot see.
// os.FileInfo on Windows does not carry the count, so the file is opened.
// When the count cannot be read the file is treated as linked: fail closed.
func HardLinkCount(path string, _ os.FileInfo) uint64 {
	f, err := os.Open(path)
	if err != nil {
		return 2
	}
	defer f.Close()
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		return 2
	}
	return uint64(d.NumberOfLinks)
}
