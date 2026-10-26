package cmd

import (
	"bytes"
	"testing"
)

func TestPrintAssetSizes(t *testing.T) {
	var out bytes.Buffer
	printAssetSizes(&out,
		[]SetupAsset{
			{LogicalRoot: "provider_claude", LogicalPath: "settings.json", ContentPresent: true, Content: []byte(`{"a":1}`)},
			{LogicalRoot: "provider_claude", LogicalPath: "keybindings.json", Missing: true},
			{LogicalRoot: "provider_codex", LogicalPath: "config.toml", Redacted: true},
		},
		[]SetupAsset{{LogicalRoot: "provider_claude_skill", LogicalPath: "review/SKILL.md", ContentPresent: true, Content: []byte("12345")}},
	)
	want := "      provider_claude/keybindings.json  missing\n" +
		"      provider_claude/settings.json  7 bytes\n" +
		"      provider_claude_skill/review/SKILL.md  5 bytes\n" +
		"      provider_codex/config.toml  redacted\n"
	if out.String() != want {
		t.Fatalf("got:\n%s\nwant:\n%s", out.String(), want)
	}
}
