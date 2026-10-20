package fsname

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CheckBeforeWrite runs Check on p, or on its nearest existing parent below
// root, before anything is created: PROGRA~1/x.md would otherwise land in an
// existing long-named directory.
func CheckBeforeWrite(root, p string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	root = filepath.Clean(root)
	for q := filepath.Clean(p); ; q = filepath.Dir(q) {
		rel, err := filepath.Rel(root, q)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil
		}
		if _, err := os.Lstat(q); err == nil {
			return Check(root, q)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
}

// CheckAfterWrite runs Check on the file just written. It never removes
// anything: when the name opened another file, that file is the user's
// long-named one, and removing it would lose it too.
func CheckAfterWrite(root, p string) error {
	if err := Check(root, p); err != nil {
		return fmt.Errorf("wrote %q, but Windows stored it under another name: %w", p, err)
	}
	return nil
}

// WriteFile is os.WriteFile with the final-name checks on both sides.
func WriteFile(root, p string, data []byte, dirPerm, perm os.FileMode) error {
	if err := CheckBeforeWrite(root, p); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), dirPerm); err != nil {
		return err
	}
	flag := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if runtime.GOOS == "windows" {
		// Absent at the check: create only, so an alias that appears
		// meanwhile fails the open instead of being overwritten.
		if _, err := os.Lstat(p); os.IsNotExist(err) {
			flag = os.O_WRONLY | os.O_CREATE | os.O_EXCL
		}
	}
	f, err := os.OpenFile(p, flag, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return CheckAfterWrite(root, p)
}

// IsWindowsDeviceName is whether Windows opens seg as a device rather than a
// file: CON, NUL, AUX, PRN, COM1-9 and LPT1-9, with any extension, any case
// and trailing dots or spaces (aux.md, Con .txt). Writing COM1 can block.
func IsWindowsDeviceName(seg string) bool {
	stem := strings.ToLower(seg)
	if i := strings.IndexByte(stem, '.'); i >= 0 {
		stem = stem[:i]
	}
	stem = strings.TrimRight(stem, " ")
	switch stem {
	case "con", "prn", "aux", "nul", "conin$", "conout$":
		return true
	}
	if len(stem) == 4 && (strings.HasPrefix(stem, "com") || strings.HasPrefix(stem, "lpt")) {
		return stem[3] >= '0' && stem[3] <= '9'
	}
	r := []rune(stem)
	if len(r) == 4 && (strings.HasPrefix(stem, "com") || strings.HasPrefix(stem, "lpt")) {
		return r[3] == '\u00b9' || r[3] == '\u00b2' || r[3] == '\u00b3'
	}
	return false
}
