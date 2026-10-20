package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// A Codex rule name comes from a bundle: "../x" must not leave rules/.
func TestSaveCodexConfigRefusesRuleNameWithSeparator(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, name := range []string{"../escape", `..\escape`, "a:b", ".."} {
		err := SaveCodexConfig(&CodexConfig{Rules: map[string]string{name: "x"}})
		if err == nil {
			t.Errorf("%q: accepted", name)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "escape.md")); err == nil {
		t.Error("escape.md written outside rules/")
	}
	if err := SaveCodexConfig(&CodexConfig{Rules: map[string]string{"draft~2": "x"}}); err != nil {
		t.Errorf("draft~2: %v", err)
	}
}
