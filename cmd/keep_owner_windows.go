//go:build windows

package cmd

import "os"

// keepOwner is a no-op: Windows files carry an ACL, not a uid/gid.
func keepOwner(_ *os.File, _ os.FileInfo) error { return nil }
