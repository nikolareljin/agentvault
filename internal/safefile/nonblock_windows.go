//go:build windows

package safefile

// A named pipe is not a path in the file system on Windows.
const openNonBlock = 0
