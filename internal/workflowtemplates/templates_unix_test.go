//go:build unix

package workflowtemplates

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestTemplateReadRefusesFIFO(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo.txt")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	done := make(chan bool, 1)
	go func() { _, ok, _ := readTemplateFile(fifo); done <- ok }()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("FIFO read as a template")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reading a FIFO template blocked")
	}
}
