package tui

import (
	"os/exec"
	"strings"
)

// findExecutable checks if a command exists and returns its path.
func findExecutable(name string) string {
	path, err := exec.LookPath(name)
	if err != nil {
		return ""
	}
	return path
}

// getVersion runs a command with a version flag and returns the output.
func getVersion(cmd, flag string) string {
	out, err := exec.Command(cmd, flag).Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
