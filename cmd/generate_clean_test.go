package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
)

// generate env --format shell and run --env output is eval'd or sourced: a
// value from a bundle must run nothing and come back byte for byte, in
// every POSIX shell on the machine.
func TestShellExportLineRoundTrips(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	mark := filepath.Join(dir, "ran")
	values := []string{
		"$(touch " + mark + ")`touch " + mark + "`\"\\$HOME",
		"it's '' a 'quote'",
		"line1\nline2\r\n\tend\n",
		"!! !$ ${HOME} \\n é 日本 \u2028",
		"",
	}
	shells := []string{}
	for _, sh := range []string{"sh", "bash", "dash", "zsh"} {
		if _, err := exec.LookPath(sh); err == nil {
			shells = append(shells, sh)
		}
	}
	for _, value := range values {
		line := shellExportLine("X", value)
		file := filepath.Join(dir, "env.sh")
		if err := os.WriteFile(file, []byte(line), 0o600); err != nil {
			t.Fatal(err)
		}
		for _, sh := range shells {
			for _, how := range []string{". " + file, "eval \"$(cat " + file + ")\"", line} {
				out, err := exec.Command(sh, "-c", how+"\nprintf %s \"$X\"").Output()
				if err != nil {
					t.Fatalf("%s %q: %v", sh, how, err)
				}
				if string(out) != value {
					t.Errorf("%s %q: value = %q, want %q", sh, how, out, value)
				}
			}
		}
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("quoted value ran a command")
	}
	for _, bad := range []string{"a\x1b[2Jb", "a\rb", "a\u009bb", "a\x00b"} {
		if got := shellExportLine("X", bad); !strings.HasPrefix(got, "# X left out") {
			t.Errorf("%q: got %q", bad, got)
		}
	}
}

// A name saved before control characters were refused must not start a
// new line, and so a command, in the generated shell file.
func TestGenerateEnvShellNameStaysInComment(t *testing.T) {
	a := testAgent()
	a.Name = "old\nexport PWN=1 #"
	a.Model = "m\x1b]0;t\a"
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{a}, nil)
	useConfigDir(t, dir)
	withFlags(t, generateEnvCmd, map[string]string{"format": "shell"})
	out, err := captureStdoutForTest(t, func() error {
		return generateEnvCmd.RunE(generateEnvCmd, []string{"-"})
	})
	if err == nil || !strings.HasPrefix(err.Error(), "1 value(s) left out") {
		t.Fatalf("err = %v, want 1 value left out", err)
	}
	if strings.Contains(out, "\nexport PWN") || strings.ContainsAny(out, "\x1b\a") {
		t.Errorf("output = %q", out)
	}
}

// run --env output is meant for eval; a model from a bundle must not run
// a command there.
func TestRunEnvRunsNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	mark := filepath.Join(t.TempDir(), "ran")
	a := testAgent()
	a.Model = "$(touch " + mark + ")"
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{a}, nil)
	useConfigDir(t, dir)
	withFlags(t, runCmd, map[string]string{"env": "true"})
	out, err := captureStdoutForTest(t, func() error { return runCmd.RunE(runCmd, []string{a.Name}) })
	if err != nil {
		t.Fatal(err)
	}
	got, err := exec.Command("sh", "-c", out+"\nprintf %s \"$AGENT_MODEL\"").Output()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("run --env output ran a command")
	}
	if string(got) != a.Model {
		t.Errorf("AGENT_MODEL = %q, want %q", got, a.Model)
	}
}

// A dotenv file from generate env is sourced by shells too; a model from a
// bundle must stay a literal value there.
func TestGenerateDotenvRunsNothing(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	mark := filepath.Join(t.TempDir(), "ran")
	a := testAgent()
	a.Model = "$(touch " + mark + ")`touch " + mark + "`$HOME\\"
	a.BaseURL = "https://h.example/it's"
	a.APIKey = "k${HOME}"
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{a}, nil)
	useConfigDir(t, dir)
	withFlags(t, generateEnvCmd, map[string]string{"format": "dotenv"})
	out, err := captureStdoutForTest(t, func() error {
		return generateEnvCmd.RunE(generateEnvCmd, []string{"-"})
	})
	if err == nil || !strings.HasPrefix(err.Error(), "2 value(s) left out") {
		t.Fatalf("err = %v, want 2 values left out", err)
	}
	prefix := envPrefixForAgent(a)
	got, err := exec.Command("env", "-i", "HOME=/nonexistent", "sh", "-c", out+"\nprintf %s \"$"+prefix+"_MODEL|${"+prefix+"_BASE_URL-unset}|${"+prefix+"_API_KEY-unset}\"").Output()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("sourcing the dotenv file ran a command")
	}
	if want := a.Model + "|unset|unset"; string(got) != want {
		t.Errorf("env = %q, want %q (file %q)", got, want, out)
	}
}

// The provider is not validated on import, so it is as hostile as the
// name: "claude;cmd;" made generate env print `export CLAUDE;CMD;_X_MODEL=`.
func TestEnvPrefixForAgentIsAName(t *testing.T) {
	for _, p := range []string{"claude;touch PWNED;", "$(id)", "a b`c`", "", "-", "9x"} {
		got := envPrefixForAgent(agent.Agent{Name: "x'y", Provider: agent.Provider(p)})
		for _, r := range got {
			if !(r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
				t.Fatalf("provider %q: prefix %q has %q", p, got, r)
			}
		}
		if got == "" || got[0] >= '0' && got[0] <= '9' || got[0] == '_' {
			t.Errorf("provider %q: prefix %q is not a variable name", p, got)
		}
	}
	if got := envPrefixForAgent(agent.Agent{Name: "my-claude", Provider: "claude"}); got != "CLAUDE_MY_CLAUDE" {
		t.Errorf("prefix = %q, want CLAUDE_MY_CLAUDE", got)
	}
}

// A left-out value must not leave an older export of the same name in
// effect: an API key left out would otherwise quietly use a stale key.
func TestShellExportLineLeftOutUnsets(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	line := shellExportLine("AV_T_KEY", "new\x1b[2Jkey")
	got, err := exec.Command("env", "-i", "AV_T_KEY=stale", "sh", "-c", line+"printf %s \"${AV_T_KEY-unset}\"").Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "unset" {
		t.Errorf("AV_T_KEY = %q after %q, want unset", got, line)
	}
}

// An env writer that leaves a value out still prints the rest but fails,
// so a script does not take a config without its API key as whole.
func TestEnvWritersExitOneWhenLeftOut(t *testing.T) {
	for _, bad := range []bool{false, true} {
		a := testAgent()
		if bad {
			a.APIKey = "k\x1b[2J"
		}
		root := t.TempDir()
		setTestHomeDir(t, root)
		t.Setenv(VaultPasswordEnv, e2eMasterPassword)
		dir := filepath.Join(root, config.AppName)
		seedProfileVault(t, dir, []agent.Agent{a}, nil)
		useConfigDir(t, dir)
		withFlags(t, runCmd, map[string]string{"env": "true"})
		out, err := captureStdoutForTest(t, func() error { return runCmd.RunE(runCmd, []string{a.Name}) })
		if (err != nil) != bad || !strings.Contains(out, "AGENT_MODEL=") {
			t.Errorf("run --env bad=%v: err = %v, out = %q", bad, err, out)
		}
		for _, format := range []string{"shell", "dotenv"} {
			withFlags(t, generateEnvCmd, map[string]string{"format": format})
			f := filepath.Join(root, "env-"+format)
			_, err := captureStdoutForTest(t, func() error { return generateEnvCmd.RunE(generateEnvCmd, []string{f}) })
			data, rerr := os.ReadFile(f)
			if (err != nil) != bad || rerr != nil || !strings.Contains(string(data), "_MODEL=") {
				t.Errorf("generate env %s bad=%v: err = %v, file %q", format, bad, err, data)
			}
		}
	}
}

// generate env --no-keys strips the credentials from a base URL, in every
// format, and keeps the rest of it usable; a plain one stays as it is.
func TestGenerateEnvNoKeysStripsBaseURLCredentials(t *testing.T) {
	urls := map[string]string{
		"q":    "https://api.example/v1?api_key=CANARYURL1&model=m",
		"user": "https://CANARYURL2x0123456789ab@user.example/v1",
		"pw":   "https://u:CANARYURL3@[::1]:8443/v1",
		"enc":  "https://enc.example/v1?Api%5FKey=CANARYURL4",
	}
	var agents []agent.Agent
	for name, u := range urls {
		a := testAgent()
		a.Name = name
		a.BaseURL = u
		agents = append(agents, a)
	}
	b := testAgent()
	b.Name = "plain"
	b.BaseURL = "https://plain.example/v1"
	agents = append(agents, b)
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, agents, nil)
	useConfigDir(t, dir)
	for _, format := range []string{"dotenv", "shell", "json"} {
		withFlags(t, generateEnvCmd, map[string]string{"format": format, "no-keys": "true"})
		out, err := captureStdoutForTest(t, func() error {
			return generateEnvCmd.RunE(generateEnvCmd, []string{"-"})
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"plain.example", "api.example/v1?model=m", "user.example/v1", "[::1]:8443/v1", "enc.example/v1"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: missing %q in %q", format, want, out)
			}
		}
		if strings.Contains(out, "CANARYURL") {
			t.Errorf("%s: output = %q", format, out)
		}
	}
}

// secretString parses URLs: a password, a token-shaped user name outside
// ssh and a percent-encoded secret parameter name count; a login name does
// not.
func TestSecretStringParsesURLs(t *testing.T) {
	for s, want := range map[string]bool{
		"https://ghp_0123456789abcdefghijABCDEF@h.example/":          true,
		"https://a1b2c3d4e5f6a7b8c9d0e1f2@h.example/v1":              true,
		"https://u:p@[::1]:8080/":                                    true,
		"ssh://git:p@h.example/o/r.git":                              true,
		"https://h.example/v1?Api%5FKey=x":                           true,
		"run --url=https://a1b2c3d4e5f6a7b8c9d0e1f2@h.example/x now": true,
		"https://h.example/v1?model=x":                               false,
		"https://h.example:8443/v1":                                  false,
		"http://${HOST}:8080/v1":                                     false,
		"http://a1b2c3d4e5f6a7b8c9d0e1f2@${HOST}:8080/v1":            true,
		"http://u:p@${HOST}:8080/v1":                                 true,
		"http://git@${HOST}:8080/v1":                                 false,
		"http://${HOST}:8080/v1?token=x":                             true,
		// A login name is not a credential.
		"ssh://git@github.com/o/r.git":                false,
		"git+ssh://git@github.com/o/r.git":            false,
		"ssh://a1b2c3d4e5f6a7b8c9d0e1f2@h.example/r":  false,
		"https://oauth2@gitlab.com/o/r.git":           false,
		"https://myorg@dev.azure.com/myorg/p/_git/r":  false,
		"http://user@localhost:11434":                 false,
		"https://x-access-token@github.com/o/r.git":   false,
		"https://first.last@h.example/r.git":          false,
		"git clone ssh://git@github.com/o/r.git here": false,
	} {
		if got := secretString(s); got != want {
			t.Errorf("secretString(%q) = %v, want %v", s, got, want)
		}
	}
}

// stripURLSecrets drops only the credential: the remaining query keeps its
// order, encoding, empty and repeated values, and a login name stays.
func TestStripURLSecretsKeepsTheRest(t *testing.T) {
	for in, want := range map[string]string{
		"https://h.example/v1?z=1&token=x&a=b+c&a=%20&e=&f": "https://h.example/v1?z=1&a=b+c&a=%20&e=&f",
		"https://h.example/v1?api_key=x":                    "https://h.example/v1",
		"https://h.example/v1?Api%5FKey=x&m=1#frag":         "https://h.example/v1?m=1#frag",
		"https://h.example/cb#access_token=x&state=s":       "https://h.example/cb#state=s",
		"https://u:p@h.example:8443/a%2Fb?q=1":              "https://h.example:8443/a%2Fb?q=1",
		"https://a1b2c3d4e5f6a7b8c9d0e1f2@h.example/v1":     "https://h.example/v1",
		"https://org@dev.azure.com/org/p/_git/r?token=x":    "https://org@dev.azure.com/org/p/_git/r",
	} {
		got, ok := stripURLSecrets(in)
		if !ok || got != want {
			t.Errorf("stripURLSecrets(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}
