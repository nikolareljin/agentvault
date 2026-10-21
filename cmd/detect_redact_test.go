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
