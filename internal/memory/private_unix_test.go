//go:build !windows

package memory

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func withUmask(t *testing.T, mask int) {
	t.Helper()
	old := syscall.Umask(mask)
	t.Cleanup(func() { syscall.Umask(old) })
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s is mode %04o, want %04o", path, got, want)
	}
}

// With a permissive umask the database, its -wal and -shm and the token
// are still private: the files are created with 0600 before the driver
// touches them, not chmodded after.
func TestPrivateFilesUnderWideUmask(t *testing.T) {
	withUmask(t, 0o022)
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(filepath.Join(dir, DBFileName))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.InsertEntry(NewEntry{Kind: "note", Title: "t", SessionID: "s", Agent: "a"}, "api"); err != nil {
		t.Fatal(err)
	}
	assertMode(t, dir, 0o700)
	for _, suffix := range []string{"", "-wal", "-shm"} {
		assertMode(t, s.Path()+suffix, 0o600)
	}
	if _, err := LoadOrCreateToken(dir); err != nil {
		t.Fatal(err)
	}
	assertMode(t, filepath.Join(dir, TokenFileName), 0o600)
}

// A data directory or database file left wider by an earlier tool is
// narrowed on open.
func TestOpenNarrowsExistingModes(t *testing.T) {
	withUmask(t, 0o022)
	dir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, DBFileName)
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	assertMode(t, dir, 0o700)
	assertMode(t, path, 0o600)
}

func TestOpenRefusesNonRegularDatabasePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, DBFileName), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(dir, DBFileName)); err == nil {
		t.Fatal("a directory in place of the database was accepted")
	}
}
