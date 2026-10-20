package fsname

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CheckBeforeWrite runs Check on p, or on its nearest
// existing parent below root, before anything is created: PROGRA~1/x.md
// would otherwise land in an existing long-named directory.
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

// CheckAfterWrite runs Check on the file just written and removes it when
// Windows stored it under another name.
func CheckAfterWrite(root, p string) error {
	if err := Check(root, p); err != nil {
		_ = os.Remove(p)
		return err
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
	if err := os.WriteFile(p, data, perm); err != nil {
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
