//go:build windows

package fsname

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// Check refuses p when the file Windows opened for it is not the one
// named: an 8.3 short name (SETTIN~1.JSO) opens settings.json. Only the
// segments below root are compared; root itself may be spelled with short
// names (C:\Users\RUNNER~1\...).
func Check(root, p string) error {
	// A link resolves to its target by design; callers refuse links with
	// their own error, so this check leaves them alone.
	if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	final, err := finalPathByHandle(windows.Handle(f.Fd()))
	if err != nil {
		return fmt.Errorf("resolving the final name of %q: %w", p, err)
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return err
	}
	want := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	got := strings.Split(strings.TrimRight(final, `\`), `\`)
	if len(got) < len(want) {
		return fmt.Errorf("%q resolves to %q; refused", p, final)
	}
	got = got[len(got)-len(want):]
	for i := range want {
		// Windows drops trailing dots and spaces ("config.json." is
		// config.json); the name checks fold them the same way.
		if !strings.EqualFold(strings.TrimRight(want[i], ". "), got[i]) {
			return fmt.Errorf("%q opens %q on Windows (a short name for another file); refused", p, final)
		}
	}
	return nil
}

func finalPathByHandle(h windows.Handle) (string, error) {
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), 0)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n+1)
	}
}

// RenameNoReplace renames src to dst and fails when dst exists, under any
// name: an 8.3 alias that appeared after the check is not replaced.
func RenameNoReplace(src, dst string) error {
	from, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(from, to, 0); err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	return nil
}
