package localllm

import (
	"context"
	"errors"
)

// Engine runs in-process GGUF inference for routing classification.
// Route returns a JSON string matching the router.LLMRouterDecision wire format.
type Engine interface {
	Route(ctx context.Context, systemPrompt, userPrompt string) (string, error)
	Close()
}

// ErrNotBuilt is returned when agentvault was built without embedded inference support.
// Build with `make build-bitnet` (CGO_ENABLED=1 -tags localllm) to enable it.
var ErrNotBuilt = errors.New("embedded inference not compiled in — use 'make build-bitnet'")
