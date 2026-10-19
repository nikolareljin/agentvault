package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The default output is Claude Desktop's own config. Its other settings must
// survive a regeneration of mcpServers.
func TestMergeMCPServersKeepsOtherKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claude_desktop_config.json")
	if err := os.WriteFile(path, []byte(`{"globalShortcut":"Ctrl+Space","mcpServers":{"old":{"command":"x"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := mergeMCPServersIntoFile(path, []byte(`{"mcpServers":{"new":{"command":"y"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out, &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["globalShortcut"]) != `"Ctrl+Space"` {
		t.Fatalf("other key lost: %s", out)
	}
	var servers struct {
		MCPServers map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(out, &servers); err != nil {
		t.Fatal(err)
	}
	if _, ok := servers.MCPServers["new"]; !ok || len(servers.MCPServers) != 1 {
		t.Fatalf("mcpServers = %v, want only the generated set", servers.MCPServers)
	}
}

func TestMergeMCPServersRefusesNonObject(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mergeMCPServersIntoFile(path, []byte(`{"mcpServers":{}}`)); err == nil {
		t.Fatal("overwrote a non-JSON file")
	}
}

// json.Unmarshal of null yields a nil map and no error; writing into it panics.
func TestMergeMCPServersRefusesNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`null`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mergeMCPServersIntoFile(path, []byte(`{"mcpServers":{}}`)); err == nil {
		t.Fatal("accepted a null document")
	}
}

func TestWriteFileAtomicKeepsModeAndSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes and symlinks")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	if err := os.WriteFile(real, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(real, 0o640); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomicKeepMode(link, []byte(`{"a":1}`)); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced by a file: %v", err)
	}
	got, err := os.ReadFile(real)
	if err != nil || string(got) != `{"a":1}` {
		t.Fatalf("target = %q, %v", got, err)
	}
	if fi, _ := os.Stat(real); fi.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v, want 0640", fi.Mode().Perm())
	}
}

func TestWriteFileAtomicDanglingSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(filepath.Join("sub", "real.json"), link); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomicKeepMode(link, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced by a file: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "sub", "real.json")); err != nil || string(got) != `{}` {
		t.Fatalf("target = %q, %v", got, err)
	}
}

func TestWriteFileAtomicCreatesDirAndPrivateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "c.json")
	if err := writeFileAtomicKeepMode(path, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

func TestMergeMCPServersNewFile(t *testing.T) {
	gen := []byte(`{"mcpServers":{}}`)
	out, err := mergeMCPServersIntoFile(filepath.Join(t.TempDir(), "absent.json"), gen)
	if err != nil || string(out) != string(gen) {
		t.Fatalf("got %s, %v", out, err)
	}
}

// A dangling link to another link: the last link's target is created and
// neither link is replaced by a file.
func TestWriteFileAtomicDanglingSymlinkChain(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	link := filepath.Join(dir, "link.json")
	mid := filepath.Join(dir, "mid.json")
	if err := os.Symlink("mid.json", link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real.json", mid); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomicKeepMode(link, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	for _, l := range []string{link, mid} {
		if fi, err := os.Lstat(l); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Fatalf("%s replaced by a file: %v", l, err)
		}
	}
	if got, err := os.ReadFile(filepath.Join(dir, "real.json")); err != nil || string(got) != `{}` {
		t.Fatalf("target = %q, %v", got, err)
	}
}

func TestMergeMCPServersKeepsHTMLCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"s<":"a<b>&c"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := mergeMCPServersIntoFile(path, []byte(`{"mcpServers":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"s<": "a<b>&c"`) {
		t.Fatalf("string rewritten: %s", out)
	}
}

// A failed rename must not leave the temp file next to the config.
func TestWriteFileAtomicRemovesTempOnFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "c.json")
	if err := os.MkdirAll(filepath.Join(target, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomicKeepMode(target, []byte(`{}`)); err == nil {
		t.Fatal("renamed over a non-empty directory")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
}

// A dangling symlinked directory in the path is refused with a clear error,
// not MkdirAll's bare "file exists".
func TestWriteFileAtomicRefusesDanglingDirLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	if err := os.Symlink(filepath.Join(dir, "missing"), filepath.Join(dir, "d")); err != nil {
		t.Fatal(err)
	}
	err := writeFileAtomicKeepMode(filepath.Join(dir, "d", "sub", "c.json"), []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "symlink to a missing directory") {
		t.Fatalf("err = %v", err)
	}
}

func TestSyncDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directories cannot be synced on Windows; the caller skips it there")
	}
	if err := syncDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := syncDir(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("synced a missing directory")
	}
}

// A Notepad-saved config starts with a UTF-8 BOM; it is still a JSON object.
func TestMergeMCPServersAcceptsBOM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte("\xef\xbb\xbf{\"k\":1}"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := mergeMCPServersIntoFile(path, []byte(`{"mcpServers":{}}`))
	if err != nil || !strings.Contains(string(out), `"k": 1`) {
		t.Fatalf("got %s, %v", out, err)
	}
}

// A device is written in place, as before, not replaced by a rename.
func TestWriteMCPConfigFileDevice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no /dev/null")
	}
	if err := writeMCPConfigFile(os.DevNull, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(os.DevNull); err != nil || fi.Mode()&os.ModeDevice == 0 {
		t.Fatalf("%s replaced: %v", os.DevNull, err)
	}
}

// A dangling relative link with ".." after a directory link resolves ".."
// from the directory the link points to, as the OS does.
func TestWriteFileAtomicDanglingSymlinkDotDot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("a", "b"), filepath.Join(dir, "dl")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink("dl/../real.json", link); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomicKeepMode(link, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(link); err != nil {
		t.Fatalf("link still dangling: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "real.json")); err == nil {
		t.Fatal("wrote the lexically cleaned path")
	}
}

// The temp file goes next to the resolved target, so the rename stays on one
// filesystem; here the lexical parent is read-only.
func TestWriteFileAtomicDanglingSymlinkTempInTargetDir(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions that bind the user")
	}
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.MkdirAll(filepath.Join(dir, "w", "b"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(ro, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "w", "b"), filepath.Join(ro, "dl")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ro, "link.json")
	if err := os.Symlink("dl/../real.json", link); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(ro, 0o700) })
	if err := writeFileAtomicKeepMode(link, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "w", "real.json")); err != nil {
		t.Fatal(err)
	}
}

// Claude Desktop's file keeps its key order, and numbers keep their text.
func TestMergeMCPServersKeepsOrderAndNumbers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	in := `{"z":1,"mcpServers":{"old":{}},"a":12345678901234567890,"z":2}`
	if err := os.WriteFile(path, []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := mergeMCPServersIntoFile(path, []byte(`{"mcpServers":{"n":{"command":"y"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"z\": 2,\n  \"mcpServers\": {\n    \"n\": {\n      \"command\": \"y\"\n    }\n  },\n  \"a\": 12345678901234567890\n}"
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}
}
