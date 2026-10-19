package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestIsInterpreterAllow(t *testing.T) {
	blanket := []string{"Bash", "Bash()", "Bash(*)", "Bash(bash:*)", "Bash(/usr/bin/python3:*)",
		"Bash(python3.12 *)", "Bash(env:*)", `Bash(C:\Windows\System32\cmd.exe:*)`, "Bash(sudo:*)", "Bash(npx:*)",
		"Bash(b*sh:*)", "Bash(?ython3:*)", "Bash(FOO=1 bash:*)", "Bash(nice bash:*)", "Bash(time python3 *)",
		"Bash(command sh:*)", "Bash($SHELL:*)"}
	for _, r := range blanket {
		if !isInterpreterAllow(r) {
			t.Errorf("%s not flagged", r)
		}
	}
	for _, r := range []string{"Bash(git status:*)", "Bash(go test:*)", "Read(~/x)", "Bash(make test)"} {
		if isInterpreterAllow(r) {
			t.Errorf("%s flagged", r)
		}
	}
}

func TestDropsBypassPermissionsMode(t *testing.T) {
	out, dropped := dropInterpreterAllows([]byte(`{"permissions":{"defaultMode":"bypassPermissions"}}`))
	if len(dropped) != 1 || bytes.Contains(out, []byte("bypassPermissions")) {
		t.Errorf("dropped=%v out=%s", dropped, out)
	}
}

// A second spelling of settings.json must not slip past the filter.
func TestApplyRefusesUncleanProviderPath(t *testing.T) {
	home := t.TempDir()
	for _, p := range []string{"./settings.json", "x/../settings.json", " settings.json"} {
		applied, _, err := applyProviderAssetsToSystem(home, []SetupAsset{{LogicalRoot: setupAssetRootProviderClaude,
			LogicalPath: p, ContentPresent: true, Content: []byte(`{"permissions":{"allow":["Bash(*)"]}}`)}})
		if err != nil {
			t.Fatal(err)
		}
		if applied != 0 {
			t.Errorf("%q applied", p)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Error("settings.json written through an unclean path")
	}
}

// SaveClaudeConfig writes settings.json from the provider config; the same
// filter applies there.
func TestFilterClaudeCustomSettings(t *testing.T) {
	c := &agent.ClaudeConfig{CustomSettings: map[string]any{
		"permissions": map[string]any{"allow": []any{"Bash(bash:*)", "Bash(git log:*)"}},
	}}
	dropped := filterClaudeCustomSettings(c)
	if len(dropped) != 1 {
		t.Fatalf("dropped = %v", dropped)
	}
	allow := c.CustomSettings["permissions"].(map[string]any)["allow"].([]any)
	if len(allow) != 1 || allow[0] != "Bash(git log:*)" {
		t.Errorf("allow = %v", allow)
	}
}

func TestIsInterpreterAllowSegmentsAndArguments(t *testing.T) {
	for _, r := range []string{"Bash(ls && bash:*)", "Bash(git log | sh)", "Bash(true; python3 x)",
		"Bash(nice -n 5 bash:*)", "Bash(nice:*)", "Bash(echo $(bash))"} {
		if !isInterpreterAllow(r) {
			t.Errorf("%s not flagged", r)
		}
	}
	for _, r := range []string{"Bash(grep -r env:*)", "Bash(git commit -m time)", "Bash(make:*)", "Bash(rg python)"} {
		if isInterpreterAllow(r) {
			t.Errorf("%s flagged", r)
		}
	}
}
