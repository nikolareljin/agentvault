//go:build windows

package cmd

// openNonBlock is not needed on Windows: a named pipe is not a path in the
// file tree there.
const openNonBlock = 0
