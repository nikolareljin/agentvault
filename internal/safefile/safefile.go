// Package safefile reads a local file that must be a regular file of
// bounded size, so a pipe, a device such as /dev/zero or a huge file in its
// place cannot block the program or exhaust memory.
package safefile

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// MaxBytes is the default cap: a vault, a settings file, an instruction
// file or a bundle is far smaller.
const MaxBytes = 256 << 20

// ErrNotRegular is returned for a pipe, device, socket or directory.
var ErrNotRegular = errors.New("not a regular file")

// ErrTooLarge is returned for a file over the cap.
var ErrTooLarge = errors.New("larger than the size cap")

// Read reads path, following symlinks, when it is a regular file of at most
// MaxBytes. A missing file gives an error that matches os.ErrNotExist.
func Read(path string) ([]byte, error) {
	return ReadMax(path, MaxBytes)
}

// ReadMax is Read with its own cap.
func ReadMax(path string, max int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: %w", path, ErrNotRegular)
	}
	if info.Size() > max {
		return nil, fmt.Errorf("%s: %w (%d bytes)", path, ErrTooLarge, max)
	}
	// Non-blocking, so a pipe swapped in after the check does not wait for
	// a writer; the descriptor is checked again below.
	f, err := os.OpenFile(path, os.O_RDONLY|openNonBlock, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	got, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !got.Mode().IsRegular() || !os.SameFile(info, got) {
		return nil, fmt.Errorf("%s: %w (changed after it was checked)", path, ErrNotRegular)
	}
	data, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("%s: %w (grew while read)", path, ErrTooLarge)
	}
	return data, nil
}
