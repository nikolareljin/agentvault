package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolvePromptHistoryPath_UsesConfigOverride(t *testing.T) {
	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("config", "")
	})
	if err := rootCmd.PersistentFlags().Set("config", "/tmp/agentvault-custom"); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}
	got := resolvePromptHistoryPath()
	want := filepath.Join("/tmp/agentvault-custom", "prompt-history.jsonl")
	if got != want {
		t.Fatalf("resolvePromptHistoryPath() = %q, want %q", got, want)
	}
}

func TestRunTemplatesShow_AcceptsResolvedCustomFilename(t *testing.T) {
	cfgDir := t.TempDir()
	repoDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cfgDir, "templates"), 0700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	customName := "custom-pr-template.txt"
	if err := os.WriteFile(filepath.Join(cfgDir, "templates", customName), []byte("custom pr body\n"), 0600); err != nil {
		t.Fatalf("WriteFile(custom): %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "templates", "metadata.json"), []byte(`{
  "schema_version": "1",
  "filenames": {"implement_pr": "`+customName+`"}
}`), 0600); err != nil {
		t.Fatalf("WriteFile(metadata): %v", err)
	}

	t.Cleanup(func() {
		_ = rootCmd.PersistentFlags().Set("config", "")
	})
	if err := rootCmd.PersistentFlags().Set("config", cfgDir); err != nil {
		t.Fatalf("setting config flag: %v", err)
	}

	cmd := templatesShowCmd
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	if err := cmd.Flags().Set("repo", repoDir); err != nil {
		t.Fatalf("setting repo flag: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Flags().Set("repo", "")
		cmd.SetOut(nil)
		cmd.SetErr(nil)
	})

	if err := runTemplatesShow(cmd, []string{customName}); err != nil {
		t.Fatalf("runTemplatesShow() error = %v", err)
	}
	if !strings.Contains(buf.String(), "custom pr body") {
		t.Fatalf("runTemplatesShow() output = %q, want custom template content", buf.String())
	}
}
