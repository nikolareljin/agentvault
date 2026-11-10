package memory

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLoadOrCreateToken(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	first, err := LoadOrCreateToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 40 || strings.ContainsAny(first, " \t\r\n") {
		t.Fatalf("token %q is not 32 URL-safe base64 bytes", first)
	}
	again, err := LoadOrCreateToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Fatal("second start minted a new token")
	}
	path := filepath.Join(dir, TokenFileName)
	if runtime.GOOS != "windows" {
		info, _ := os.Stat(path)
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("token file mode %04o, want 0600", mode)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateToken(dir); err == nil || !strings.Contains(err.Error(), "chmod 600") {
			t.Fatalf("a world-readable token file was trusted: %v", err)
		}
		os.Chmod(path, 0o600)
	}
	if err := os.WriteFile(path, []byte("short\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateToken(dir); err == nil || !strings.Contains(err.Error(), "not a token this tool wrote") {
		t.Fatalf("a short token file was trusted: %v", err)
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 20)+" "+strings.Repeat("b", 20)+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateToken(dir); err == nil {
		t.Fatal("a token with a space was trusted")
	}
}

func TestLoadOrCreateTokenRefusesNonRegularFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, TokenFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateToken(dir); err == nil {
		t.Fatal("a directory in place of the token file was accepted")
	}
}
