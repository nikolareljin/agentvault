package cmd

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/config"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

func TestMaskURLSecrets(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"https://host/v1", "https://host/v1"},
		{"http://localhost:11434", "http://localhost:11434"},
		{"https://user:pass@host/v1", "https://user:***@host/v1"},
		{"https://user:pass@host:8443/v1?x=1", "https://user:***@host:8443/v1?x=1"},
		{"https://tok3n%2Fx@host/v1", "https://***@host/v1"},
		{"https://host/v1?api_key=abc&x=1", "https://host/v1?api_key=***&x=1"},
		{"https://host/v1?x=1#access_token=abc", "https://host/v1?x=1#access_token=***"},
		{"https://u:p@host/v1?token=abc", "https://u:***@host/v1?token=***"},
		{"https://git@dev.azure.com/git/x", "https://git@dev.azure.com/git/x"},
		{"https://user:pa ss@host/%zz", "<secret>"},
		{"https://u:p@host/sk-abcdefghijklmnopqrstuvwxyz0123", "<secret>"},
		{"https://host/v1?a=1;token=abc", "<secret>"},
		{"https://p%40ss@host/v1", "https://***@host/v1"},
		{"https://user:p@ss@host/v1", "https://user:***@host/v1"},
		{"https://abcdef0123456789abcdef0123:x-oauth-basic@github.com/o/r", "https://***@github.com/o/r"},
		{"https://tok:@host/v1", "https://***@host/v1"},
		{"https://tok:x@host/v1", "https://***@host/v1"},
		{"https://abc123def456ghi789jkl:pw@host/v1", "https://***@host/v1"},
		{"https://oauth2:glpat-secret@gitlab.example/o/r", "https://oauth2:***@gitlab.example/o/r"},
		{"https://first.last-name_x:pw@host/v1", "https://first.last-name_x:***@host/v1"},
	}
	for _, tc := range cases {
		if got := maskURLSecrets(tc.in); got != tc.want {
			t.Errorf("maskURLSecrets(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// No command that prints an agent's base URL or a capability endpoint
// shows the password in it.
func TestCommandsMaskBaseURLPassword(t *testing.T) {
	const canary = "Canary7pw"
	raw := "https://user:" + canary + "@host.example/v1?api_key=" + canary + "q"
	const masked = "https://user:***@host.example/v1?api_key=***"
	a := testAgent()
	a.BaseURL = raw
	root := t.TempDir()
	setTestHomeDir(t, root)
	t.Setenv(VaultPasswordEnv, e2eMasterPassword)
	dir := filepath.Join(root, config.AppName)
	seedProfileVault(t, dir, []agent.Agent{a}, nil)
	v := vault.New(filepath.Join(dir, config.VaultFile))
	if err := v.Unlock(e2eMasterPassword); err != nil {
		t.Fatal(err)
	}
	if err := v.AddCapability(agent.ModelCapabilityEntry{EndpointURL: raw, ModelName: "m"}); err != nil {
		t.Fatal(err)
	}
	useConfigDir(t, dir)
	cases := []struct {
		name  string
		cmd   *cobra.Command
		args  []string
		flags map[string]string
	}{
		{"run", runCmd, []string{a.Name}, nil},
		{"list json", listCmd, nil, map[string]string{"json": "true"}},
		{"capability list", capabilityListCmd, nil, nil},
		{"capability list json", capabilityListCmd, nil, map[string]string{"json": "true"}},
	}
	for _, tc := range cases {
		withFlags(t, tc.cmd, tc.flags)
		out, err := captureStdoutForTest(t, func() error { return tc.cmd.RunE(tc.cmd, tc.args) })
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if strings.Contains(out, canary) {
			t.Errorf("%s printed the URL password: %s", tc.name, out)
		}
		if !strings.Contains(out, masked) {
			t.Errorf("%s did not print the masked URL: %s", tc.name, out)
		}
	}

	// --show-keys asks for the credentials, as it does for API keys.
	withFlags(t, listCmd, map[string]string{"json": "true", "show-keys": "true"})
	out, err := captureStdoutForTest(t, func() error { return listCmd.RunE(listCmd, nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, raw) {
		t.Errorf("list --show-keys did not print the stored URL: %s", out)
	}
}

func TestServeMasksBaseURLPassword(t *testing.T) {
	v := testServeVault(t)
	a, _ := v.Get("claude-main")
	a.BaseURL = "https://user:Canary7pw@host.example/v1"
	if err := v.Update(a); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(newServeMux(v, "/tmp/vault.enc", "secret-key"))
	defer srv.Close()
	for _, path := range []string{"/api/v1/agents", "/api/v1/agents/claude-main"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer secret-key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), "Canary7pw") || !strings.Contains(string(body), "https://user:***@host.example/v1") {
			t.Errorf("%s: base URL not masked: %s", path, body)
		}
	}
}

// A request error quotes its URL; the credentials in it are masked.
func TestEndpointErrorsMaskURLCredentials(t *testing.T) {
	const canary = "Canary7pw"
	errs := map[string]error{
		"openai validate": validateOpenAIEndpoint("http://127.0.0.1:1/v1?api_key="+canary, "", time.Second),
		"ollama validate": validateOllamaEndpoint("http://127.0.0.1:1?token="+canary, time.Second, "check"),
		"openai parse":    func() error { _, err := openAIEndpointURL("http://u:"+canary+"@h\x7f/v1", "models"); return err }(),
	}
	for name, err := range errs {
		if err == nil {
			t.Errorf("%s: no error", name)
			continue
		}
		if strings.Contains(err.Error(), canary) {
			t.Errorf("%s: error shows the credential: %v", name, err)
		}
	}
}
