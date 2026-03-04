package cmd

import (
	"path/filepath"
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
