package cmd

import (
	"fmt"
	"os"
)

// maxImportFileBytes caps a file an import reads: a bundle, a profile, a
// session or an instructions file. Anything larger is refused before it is
// read, so a huge file or a device such as /dev/zero cannot exhaust memory.
const maxImportFileBytes = 256 << 20

// readImportFile reads the file an import was given. It must be a regular
// file of at most maxImportFileBytes.
func readImportFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", printable(path))
	}
	if info.Size() > maxImportFileBytes {
		return nil, fmt.Errorf("%s is larger than %d bytes", printable(path), maxImportFileBytes)
	}
	data, reason, err := readStattedFile(path, info, maxImportFileBytes)
	if err != nil {
		return nil, err
	}
	if reason != "" {
		return nil, fmt.Errorf("%s %s", printable(path), reason)
	}
	return data, nil
}
