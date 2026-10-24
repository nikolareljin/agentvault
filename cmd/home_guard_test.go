package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/crypto/cryptotest"
)

// TestMain points every home and config variable at a throwaway directory
// before any test runs, so no test can write to the real user's agent configs.
func TestMain(m *testing.M) {
	cryptotest.UseFastParams()
	os.Exit(runWithTempHome(m))
}

func runWithTempHome(m *testing.M) int {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		// A helper process started by a test: HOME is already that test
		// binary's throwaway home, and the helper ends with os.Exit, so a
		// home made here would never be removed.
		return m.Run()
	}
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

// A helper process leaves no throwaway home behind.
func TestHelperProcessLeavesNoTempHome(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		return
	}
	tmp := t.TempDir()
	helper := exec.Command(os.Args[0], "-test.run=TestPromptWorkflowHelperProcess", "--", "git", "x")
	helper.Env = append(os.Environ(), "GO_WANT_HELPER_PROCESS=1", "PROMPT_WORKFLOW_HELPER_FAIL=1", "TMPDIR="+tmp, "TMP="+tmp, "TEMP="+tmp)
	_ = helper.Run()
	left, err := filepath.Glob(filepath.Join(tmp, "agentvault-test-home-*"))
	if err != nil || len(left) > 0 {
		t.Errorf("helper left %v (%v)", left, err)
	}
}
