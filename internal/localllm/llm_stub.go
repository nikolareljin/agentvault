//go:build !localllm

package localllm

import "context"

// New returns ErrNotBuilt when the binary was not compiled with -tags localllm.
func New(_ string, _, _, _ int) (Engine, error) {
	return nil, ErrNotBuilt
}

type stubEngine struct{}

func (s *stubEngine) Route(_ context.Context, _, _ string) (string, error) {
	return "", ErrNotBuilt
}

func (s *stubEngine) Close() {}
