package vault

import (
	"testing"

	"github.com/nikolareljin/agentvault/internal/agent"
)

func TestNew(t *testing.T) {
	v := New("/tmp/test.enc")
	if v.Path() != "/tmp/test.enc" {
		t.Errorf("Path() = %q, want %q", v.Path(), "/tmp/test.enc")
	}
}

func TestAddAndList(t *testing.T) {
	v := New("/tmp/test.enc")
	a := agent.Agent{Name: "test", Provider: agent.ProviderClaude}
	if err := v.Add(a); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	agents := v.List()
	if len(agents) != 1 {
		t.Fatalf("List() len = %d, want 1", len(agents))
	}
	if agents[0].Name != "test" {
		t.Errorf("agent name = %q, want %q", agents[0].Name, "test")
	}
}
