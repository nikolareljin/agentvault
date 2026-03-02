package cmd

import "testing"

func TestParseTUIInvocation_DefaultNoArgs(t *testing.T) {
	launch, target, err := parseTUIInvocation(nil)
	if err != nil {
		t.Fatalf("parseTUIInvocation(nil) error = %v", err)
	}
	if !launch {
		t.Fatalf("launch = false, want true")
	}
	if target != "agents" {
		t.Fatalf("target = %q, want agents", target)
	}
}

func TestParseTUIInvocation_NoTUIFlagWithCommand(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"list"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(list) error = %v", err)
	}
	if launch {
		t.Fatalf("launch = true, want false (target=%q)", target)
	}
}

func TestParseTUIInvocation_FlagOnly(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"-t"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(-t) error = %v", err)
	}
	if !launch || target != "agents" {
		t.Fatalf("launch,target = %v,%q want true,agents", launch, target)
	}
}

func TestParseTUIInvocation_ExplicitTarget(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"--tui", "commands"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(--tui commands) error = %v", err)
	}
	if !launch || target != "commands" {
		t.Fatalf("launch,target = %v,%q want true,commands", launch, target)
	}
}

func TestParseTUIInvocation_InferTargetFromCommand(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"detect", "add", "-t"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(detect add -t) error = %v", err)
	}
	if !launch || target != "detected" {
		t.Fatalf("launch,target = %v,%q want true,detected", launch, target)
	}
}

func TestParseTUIInvocation_InferTargetWithConfigFlag(t *testing.T) {
	launch, target, err := parseTUIInvocation([]string{"--config", "/tmp/agentvault", "detect", "add", "-t"})
	if err != nil {
		t.Fatalf("parseTUIInvocation(--config ... detect add -t) error = %v", err)
	}
	if !launch || target != "detected" {
		t.Fatalf("launch,target = %v,%q want true,detected", launch, target)
	}
}

func TestParseTUIInvocation_InvalidTarget(t *testing.T) {
	_, _, err := parseTUIInvocation([]string{"--tui", "invalid-target"})
	if err == nil {
		t.Fatalf("expected invalid target error")
	}
}
