package tui

import (
	"context"
	"os/exec"
	"strings"
	"time"
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
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, cmd, flag).CombinedOutput()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}
