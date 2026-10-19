//go:build windows

package cmd

import "os"

// hardLinkCount reports 1: os.FileInfo on Windows does not carry a link count.
func hardLinkCount(_ os.FileInfo) uint64 { return 1 }
