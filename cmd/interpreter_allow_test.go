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
		"Bash(command sh:*)", "Bash($SHELL:*)", "Bash(py:*)", "Bash(tcsh:*)", "Bash(wsl:*)", "Bash(tsx *)", "Bash(Rscript:*)"}
	for _, n := range []string{"sg", "firejail", "xvfb-run", "parallel", "Rscript", "R", "julia", "lua", "php", "ruby", "perl", "node", "deno", "bun", "tclsh", "wish", "pwsh", "powershell", "cmd", "osascript", "busybox", "toybox", "sudo", "doas", "su", "runuser", "setpriv", "chrt", "ionice", "taskset", "stdbuf", "nohup", "timeout", "env", "gnome-terminal", "konsole", "xterm", "tmux", "screen"} {
		blanket = append(blanket, "Bash("+n+":*)")
	}
	for _, r := range blanket {
		if !isInterpreterAllow(r) {
			t.Errorf("%s not flagged", r)
		}
	}
	for _, r := range []string{"Bash(git status:*)", "Bash(go test:*)", "Read(~/x)", "Bash(make test)", "Bash(git:*)", "Bash(go:*)", "Bash(make:*)", "Bash(npm run:*)", "Bash(ls:*)", "Bash(find .)", "Bash(grep -E:*)", "Bash(tar -C:*)"} {
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
	for _, p := range []string{"./settings.json", "x/../settings.json", " settings.json", "settings.json::$DATA"} {
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
		"Bash(nice -n 5 bash:*)", "Bash(nice:*)", "Bash(echo $(bash))",
		"Bash(if true; then bash; fi)", "Bash(! bash)", "Bash(nice -n hd bash)", "Bash(timeout 10s ./run)",
		"Bash(find . -exec rm {} +)", "Bash(git -c core.pager=x log)", "Bash(nice -n 5 make bash)",
		"Bash(timeout 5:*)", "Bash(nice -n 5 :*)", "Bash(nice -n 5 make:*)", "Bash('bash' -c x)", `Bash(b\ash:*)`,
		`Bash("python3" x)`, "Bash(ba''sh:*)", "Bash(py*)", "Bash(ls; ba*)",
		"Bash(time:*)", "Bash(! :*)", "Bash(if:*)", "Bash(git  status)", "Bash(ls > /etc/x)", "Bash(FOO=1 make)",
		"Bash(*:*)", "Bash(ls; *)", "Bash(ls && *:*)",
		"Bash(. ./env.sh)", "Bash(source:*)", "Bash(builtin:*)", "Bash(awk:*)", "Bash(gawk x)", "Bash(mawk x)",
		"Bash(npm exec:*)", "Bash(npx:*)", "Bash(pnpm dlx:*)", "Bash(yarn dlx:*)", "Bash(bunx:*)", "Bash(uvx:*)",
		"Bash(pipx run:*)", "Bash(find . -ok rm)", "Bash(find . -okdir rm)", "Bash(unshare:*)", "Bash(nsenter:*)",
		"Bash(systemd-run:*)", "Bash(pkexec:*)", "Bash(doas:*)", "Bash(chroot:*)", "Bash(setsid:*)",
		"Bash(flock:*)", "Bash(watch:*)", "Bash(script:*)", "Bash(expect:*)", "Bash(osascript:*)",
		"Bash(.. x)", "Bash(.hidden/run)"} {
		if !isInterpreterAllow(r) {
			t.Errorf("%s not flagged", r)
		}
	}
	for _, r := range []string{"Bash(make:*)", "Bash(git status:*)", "Bash(go test ./...)", "Bash(make test)", "Bash(npm run test *)", "Bash(./scripts/check.sh)", "Bash(../bin/lint:*)", "Bash(ls ./docs)",
		"Bash(find . -name x)", "Bash(ls ..)", "Bash(git add .)"} {
		if isInterpreterAllow(r) {
			t.Errorf("%s flagged", r)
		}
	}
}

// Upper-case flags such as git -C and grep -E are not exec flags.
func TestIsInterpreterAllowUpperCaseFlags(t *testing.T) {
	for _, r := range []string{"Bash(git -C sub status)", "Bash(grep -E foo:*)", "Bash(tar -C out -xf a.tar)"} {
		if isInterpreterAllow(r) {
			t.Errorf("%s flagged", r)
		}
	}
	for _, r := range []string{"Bash(git -c x=y:*)", "Bash(sed -e p:*)"} {
		if !isInterpreterAllow(r) {
			t.Errorf("%s not flagged", r)
		}
	}
}

// Windows writes "settings.json." to settings.json: the filter must see it.
func TestApplyFiltersTrailingDotSettings(t *testing.T) {
	home := t.TempDir()
	content, _, err := applyAndRead(t, home, "settings.json.")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(content, []byte("Bash(*)")) {
		t.Errorf("Bash(*) kept in %q: %s", "settings.json.", content)
	}
}

func applyAndRead(t *testing.T, home, name string) ([]byte, int, error) {
	t.Helper()
	applied, _, err := applyProviderAssetsToSystem(home, []SetupAsset{{LogicalRoot: setupAssetRootProviderClaude,
		LogicalPath: name, ContentPresent: true, Content: []byte(`{"permissions":{"allow":["Bash(*)"]}}`)}})
	if err != nil || applied != 1 {
		return nil, applied, err
	}
	entries, _ := os.ReadDir(filepath.Join(home, ".claude"))
	for _, e := range entries {
		b, _ := os.ReadFile(filepath.Join(home, ".claude", e.Name()))
		return b, applied, nil
	}
	return nil, applied, nil
}

func TestIsInterpreterAllowMoreInterpretersAndRunners(t *testing.T) {
	for _, r := range []string{"Bash(pypy3:*)", "Bash(irb:*)", "Bash(guile:*)", "Bash(racket:*)", "Bash(elixir:*)",
		"Bash(jshell:*)", "Bash(swift:*)", "Bash(rlwrap:*)", "Bash(go run:*)", "Bash(cargo run:*)", "Bash(npm x:*)",
		"Bash(pnpm x:*)", "Bash(dotnet run:*)", "Bash(deno run:*)", "Bash(uv tool:*)"} {
		if !isInterpreterAllow(r) {
			t.Errorf("%s not flagged", r)
		}
	}
	for _, r := range []string{"Bash(go build:*)", "Bash(cargo test:*)", "Bash(npm test:*)"} {
		if isInterpreterAllow(r) {
			t.Errorf("%s flagged", r)
		}
	}
}
