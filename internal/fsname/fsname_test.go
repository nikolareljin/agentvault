package fsname

import (
	"runtime"
	"testing"
)

func TestIsWindowsDeviceName(t *testing.T) {
	devices := []string{
		"CON", "con", "Prn", "aux", "NUL", "conin$", "CONIN$", "conout$", "CONOUT$.txt",
		"COM0", "com1", "COM9", "LPT0", "lpt9", "COM¹", "lpt².md", "LPT³",
		"aux.md", "aux .md", "aux.md.", "AUX.", "con  ", "nul.tar.gz", "Com1 .txt",
	}
	for _, s := range devices {
		if !IsWindowsDeviceName(s) {
			t.Errorf("%q: not a device name", s)
		}
	}
	files := []string{
		"", "console.md", "auxiliary", "aux-notes.md", "com10", "com", "lpt", "coma",
		"com⁴", "nul_", ".con", "my.aux", "conin", "conout$x",
	}
	for _, s := range files {
		if IsWindowsDeviceName(s) {
			t.Errorf("%q: a device name", s)
		}
	}
}

// Off Windows a name opens only itself, so the write checks never refuse.
func TestCheckBeforeWriteOffWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has its own tests")
	}
	root := t.TempDir()
	if err := CheckBeforeWrite(root, root+"/SETTIN~1.JSO"); err != nil {
		t.Fatal(err)
	}
}
