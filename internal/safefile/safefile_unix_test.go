//go:build unix

package safefile

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestReadRefusesPipesDevicesAndHugeFiles(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	zero := filepath.Join(dir, "zero")
	if err := os.Symlink("/dev/zero", zero); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for _, p := range []string{fifo, zero, dir} {
			if _, err := Read(p); !errors.Is(err, ErrNotRegular) {
				t.Errorf("Read(%s) err = %v", p, err)
			}
		}
		if _, err := ReadMax(big, 5); !errors.Is(err, ErrTooLarge) {
			t.Errorf("ReadMax big err = %v", err)
		}
		if b, err := Read(big); err != nil || string(b) != "0123456789" {
			t.Errorf("Read big = %q, %v", b, err)
		}
		if _, err := Read(filepath.Join(dir, "none")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("missing err = %v", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Read blocked")
	}
}
