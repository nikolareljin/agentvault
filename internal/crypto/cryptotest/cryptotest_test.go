package cryptotest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/crypto"
)

// A vault written with FastParams must still open: ParseHeader refuses
// parameters below its bounds.
func TestFastParamsPassHeaderBounds(t *testing.T) {
	h := crypto.Header{Params: FastParams, Salt: make([]byte, crypto.SaltLen)}
	// A nonce follows the header in a real file; ParseHeader requires one.
	got, _, err := crypto.ParseHeader(append(h.Bytes(), make([]byte, crypto.NonceLen)...))
	if err != nil {
		t.Fatalf("ParseHeader refused FastParams: %v", err)
	}
	if got.Params != FastParams {
		t.Fatalf("params = %+v, want %+v", got.Params, FastParams)
	}
}

// The shipped binary must never pick up the cheap parameters.
func TestNotImportedByProductionCode(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/nikolareljin/agentvault").Output()
	if err != nil {
		// go test runs with go on PATH, so a failure here is a real one.
		t.Fatalf("go list: %v", err)
	}
	for _, p := range strings.Fields(string(out)) {
		if strings.HasSuffix(p, "/internal/crypto/cryptotest") {
			t.Fatalf("%s is in the binary's dependencies", p)
		}
	}
}

// Every test package that can reach internal/vault calls UseFastParams in
// its TestMain. One that forgets still passes, only slower: under -race on
// macOS that slowness is a 10 minute timeout. internal/crypto is exempt: it
// keeps the real parameters and pins them.
func TestVaultTestPackagesUseFastParams(t *testing.T) {
	const vaultPkg = "github.com/nikolareljin/agentvault/internal/vault"
	out, err := exec.Command("go", "list", "-f", "{{.ImportPath}}|{{.Dir}}|{{len .TestGoFiles}}", "github.com/nikolareljin/agentvault/...").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	checked := 0
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(line, "|", 3)
		if len(f) != 3 || f[2] == "0" || strings.HasSuffix(f[0], "/internal/crypto") || strings.HasSuffix(f[0], "/cryptotest") {
			continue
		}
		deps, err := exec.Command("go", "list", "-test", "-deps", f[0]).Output()
		if err != nil {
			t.Fatalf("go list -test -deps %s: %v", f[0], err)
		}
		if !slices.Contains(strings.Fields(string(deps)), vaultPkg) {
			continue
		}
		checked++
		files, _ := filepath.Glob(filepath.Join(f[1], "*_test.go"))
		found := false
		for _, name := range files {
			b, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(b), "cryptotest.UseFastParams()") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%s: tests reach internal/vault but no TestMain calls cryptotest.UseFastParams()", f[0])
		}
	}
	if checked == 0 {
		t.Fatal("no test package reaches internal/vault: the check found nothing to check")
	}
}
