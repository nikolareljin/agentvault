package gitid

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitCommandBoundsChildAndOutput(t *testing.T) {
	cmd, out := gitCommand(context.Background(), "/x/.git", "/x/c", "rev-list")
	if cmd.WaitDelay <= 0 || cmd.WaitDelay > 5*time.Second {
		t.Errorf("WaitDelay %v; a child holding stdout open would hang the call", cmd.WaitDelay)
	}
	if cmd.Stdout != out {
		t.Fatal("stdout not capped")
	}
	if _, err := out.Write(make([]byte, maxGitOutput+1)); err == nil {
		t.Error("write past the cap succeeded")
	}
}

func TestCappedBuffer(t *testing.T) {
	b := &cappedBuffer{max: 4}
	if _, err := b.Write([]byte("abc")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Write([]byte("de")); err == nil {
		t.Fatal("want overflow error")
	}
	if b.String() != "abc" {
		t.Fatalf("got %q", b.String())
	}
}

func TestExecGitRealRun(t *testing.T) {
	needGit(t)
	dir, root := newRepo(t, "")
	out, err := ExecGit(context.Background(), filepath.Join(dir, ".git"), filepath.Join(dir, ".git"), "rev-list", "--max-parents=0", "HEAD")
	if err != nil || strings.TrimSpace(string(out)) != root {
		t.Fatalf("%q %v", out, err)
	}
}
