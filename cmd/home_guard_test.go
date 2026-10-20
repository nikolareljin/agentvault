package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain points every home and config variable at a throwaway directory
// before any test runs, so no test can write to the real user's agent configs.
func TestMain(m *testing.M) {
	os.Exit(runWithTempHome(m))
}

func runWithTempHome(m *testing.M) int {
	dir, err := os.MkdirTemp("", "agentvault-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "home guard:", err)
		return 2
	}
	defer os.RemoveAll(dir)
	for key, sub := range map[string]string{
		"HOME":            "",
		"USERPROFILE":     "",
		"XDG_CONFIG_HOME": ".config",
		"XDG_DATA_HOME":   filepath.Join(".local", "share"),
		"XDG_CACHE_HOME":  ".cache",
		"XDG_STATE_HOME":  filepath.Join(".local", "state"),
		"APPDATA":         filepath.Join("AppData", "Roaming"),
		"LOCALAPPDATA":    filepath.Join("AppData", "Local"),
	} {
		if err := os.Setenv(key, filepath.Join(dir, sub)); err != nil {
			fmt.Fprintln(os.Stderr, "home guard:", err)
			return 2
		}
	}
	if home, err := os.UserHomeDir(); err != nil || home != dir {
		fmt.Fprintf(os.Stderr, "home guard: home is %q (%v), want %q\n", home, err, dir)
		return 2
	}
	return m.Run()
}

func TestHomeGuardIsolatesUserHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(home) != filepath.Clean(os.TempDir()) || !strings.HasPrefix(filepath.Base(home), "agentvault-test-home-") {
		t.Fatalf("tests run against a real home directory: %s", home)
	}
}
