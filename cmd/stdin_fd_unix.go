//go:build !windows

package cmd

import "syscall"

func stdinFD() int {
	return syscall.Stdin
}
