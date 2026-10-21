package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactSensitiveMapDropsEnvHeadersAndSecretShapes(t *testing.T) {
	in := map[string]any{
		"env":         map[string]any{"FOO": "plain-env-value"},
		"model":       "opus",
		"statusLine":  map[string]any{"command": "run --token=sk-ant-abcdefghijklmnop"},
		"permissions": map[string]any{"allow": []any{"Bash(ls)"}},
		"mcpServers": map[string]any{
			"s": map[string]any{
				"command": "srv",
				"args":    []any{"--token", "hunter2value"},
				"headers": map[string]any{"X-Id": "opaque"},
			},
		},
	}
	out, err := json.Marshal(redactSensitiveMap(in))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, leak := range []string{"plain-env-value", "sk-ant-", "hunter2value", "opaque"} {
		if strings.Contains(got, leak) {
			t.Errorf("settings_safe carries %q: %s", leak, got)
		}
	}
	for _, keep := range []string{"opus", "Bash(ls)", `"command":"srv"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("settings_safe lost %q: %s", keep, got)
		}
	}
}

func TestRedactSensitiveMapKeepsOrdinarySettings(t *testing.T) {
	in := map[string]any{
		"includeCoAuthoredBy": false,
		"maxTokens":           float64(4096),
		"permissions": map[string]any{"allow": []any{
			"Bash(ls)", "Bash(gh --token *)", "Read(~/notes)",
		}},
		"mcpServers": map[string]any{"s": map[string]any{
			"args": []any{"-y", "--api-key", "canary-a", "--port=1", "--token=canary-b", "--verbose"},
		}},
	}
	out, err := json.Marshal(redactSensitiveMap(in))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, keep := range []string{`"includeCoAuthoredBy":false`, `"maxTokens":4096`, "Bash(ls)", "Read(~/notes)", `"-y"`, `"--port=1"`, `"--verbose"`} {
		if !strings.Contains(got, keep) {
			t.Errorf("settings_safe lost %s: %s", keep, got)
		}
	}
	for _, leak := range []string{"canary-", "--api-key", "gh --token"} {
		if strings.Contains(got, leak) {
			t.Errorf("settings_safe carries %q: %s", leak, got)
		}
	}
}
