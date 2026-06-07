package localllm

import (
	"context"
	"errors"
	"testing"
)

func TestNewReturnsErrNotBuiltWithoutTag(t *testing.T) {
	_, err := New("", 0, 0, 0)
	if !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("New() error = %v, want ErrNotBuilt", err)
	}
}

func TestNewReturnsErrNotBuiltForAnyPath(t *testing.T) {
	_, err := New("/nonexistent/model.gguf", 512, 4, 0)
	if !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("New(%q) error = %v, want ErrNotBuilt", "/nonexistent/model.gguf", err)
	}
}

func TestStubEngineRouteReturnsErrNotBuilt(t *testing.T) {
	s := &stubEngine{}
	_, err := s.Route(context.Background(), "sys", "usr")
	if !errors.Is(err, ErrNotBuilt) {
		t.Fatalf("stubEngine.Route() error = %v, want ErrNotBuilt", err)
	}
}

func TestStubEngineCloseIsNoOp(t *testing.T) {
	s := &stubEngine{}
	s.Close() // must not panic
}
