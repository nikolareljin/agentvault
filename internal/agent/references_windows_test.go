//go:build windows

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// A short name (ENV~1.PRO for .env.production) is judged by the long name
// the file resolves to.
func TestReferenceShortNameJudgedByLongName(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, ".env.production")
	if err := os.WriteFile(secret, []byte("K=v\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	long, _ := windows.UTF16PtrFromString(secret)
	buf := make([]uint16, windows.MAX_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil {
		t.Fatal(err)
	}
	short := filepath.Base(windows.UTF16ToString(buf[:n]))
	if strings.EqualFold(short, ".env.production") {
		t.Skip("no 8.3 short names on this volume")
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := classifyReference(real, "", short)
	if ref.Status != ReferenceRefused {
		t.Errorf("%s: %+v, want refused", short, ref)
	}
}
