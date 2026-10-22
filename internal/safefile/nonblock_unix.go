//go:build !windows

package safefile

import "syscall"

const openNonBlock = syscall.O_NONBLOCK
