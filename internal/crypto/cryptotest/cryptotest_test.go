package cryptotest

import (
	"os/exec"
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
