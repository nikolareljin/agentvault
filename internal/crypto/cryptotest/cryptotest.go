// Package cryptotest is for tests only: production code never imports it.
package cryptotest

import "github.com/nikolareljin/agentvault/internal/crypto"

// FastParams are the cheapest Argon2id parameters ParseHeader accepts.
var FastParams = crypto.KDFParams{Time: 1, MemoryKiB: 8 * 1024, Threads: 1}

// UseFastParams makes vaults created from here on use FastParams. Tests open
// vaults hundreds of times, and under -race the default 64 MiB, t=3 Argon2id
// took the cmd package past go test's 10 minute timeout on macOS runners.
// Call it first in TestMain, before any test (or helper process) runs.
func UseFastParams() {
	crypto.CurrentParams = FastParams
}
