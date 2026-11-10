//go:build !windows

package cmd

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The whole command: database and token created under --db, the listener
// on 127.0.0.1, /healthz open, /entries behind the token from the file, a
// clean exit on SIGINT.
func TestMemoryServeRunsAndStopsOnInterrupt(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "data", "memory.db")
	ready := make(chan *net.TCPAddr, 1)
	memoryServeReady = func(addr *net.TCPAddr) { ready <- addr }
	defer func() { memoryServeReady = nil }()
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, err := runMemory(t, "--port", "0", "--db", db)
		done <- result{out, err}
	}()
	var addr *net.TCPAddr
	select {
	case addr = <-ready:
	case r := <-done:
		t.Fatalf("serve returned early: %v\n%s", r.err, r.out)
	case <-time.After(10 * time.Second):
		t.Fatal("listener never came up")
	}
	if addr.IP.String() != "127.0.0.1" {
		t.Fatalf("bound %s", addr)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(addr.Port)
	res, err := http.Get(base + "/healthz")
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("GET /healthz: %v %v", err, res)
	}
	res.Body.Close()
	tokenPath := filepath.Join(dir, "data", "memory.token")
	raw, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(tokenPath); info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode %04o", info.Mode().Perm())
	}
	if info, _ := os.Stat(db); info.Mode().Perm() != 0o600 {
		t.Fatalf("database mode %04o", info.Mode().Perm())
	}
	req, _ := http.NewRequest("GET", base+"/entries", nil)
	res, err = http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 401 {
		t.Fatalf("GET /entries without a token: %v %v", err, res)
	}
	res.Body.Close()
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(raw)))
	res, err = http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != 200 {
		t.Fatalf("GET /entries with the file's token: %v %v", err, res)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if strings.TrimSpace(string(body)) != `{"entries":[]}` {
		t.Fatalf("GET /entries = %s", body)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("serve returned %v\n%s", r.err, r.out)
		}
		if !strings.Contains(r.out, "serving http://127.0.0.1:"+strconv.Itoa(addr.Port)+"/") ||
			strings.Contains(r.out, strings.TrimSpace(string(raw))) {
			t.Fatalf("output: %s", r.out)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop on SIGINT")
	}
	if _, err := http.Get(base + "/healthz"); err == nil {
		t.Fatal("the server still answers after shutdown")
	}
}
