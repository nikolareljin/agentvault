//go:build windows

package cmd

import "os"

func stdinFD() int {
	return int(os.Stdin.Fd())
}
