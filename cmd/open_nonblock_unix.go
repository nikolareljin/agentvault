//go:build !windows

package cmd

import "syscall"

// openNonBlock keeps an open from waiting for a writer when a pipe was
// swapped in for a checked file.
const openNonBlock = syscall.O_NONBLOCK
