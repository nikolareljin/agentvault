package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectOllamaUsesOllamaBaseURLEnv(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake ollama is a shell script")
	}
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'ollama version 0.0.0'\n"
	if err := os.WriteFile(filepath.Join(bin, "ollama"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("OLLAMA_HOST", "")

	t.Setenv("OLLAMA_BASE_URL", "http://gpu.example:11434")
	if got := detectOllama().Settings["base_url"]; got != "http://gpu.example:11434" {
		t.Fatalf("base_url = %v, want the OLLAMA_BASE_URL value", got)
	}
	t.Setenv("OLLAMA_BASE_URL", "")
	if got := detectOllama().Settings["base_url"]; got != "http://localhost:11434" {
		t.Fatalf("base_url = %v, want the localhost default", got)
	}
}
