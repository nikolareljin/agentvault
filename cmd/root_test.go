package cmd

import "testing"

func TestShouldLaunchTUI_DefaultNoArgs(t *testing.T) {
	if !shouldLaunchTUI(false, nil) {
		t.Fatalf("shouldLaunchTUI(false, nil) = false, want true")
	}
}

func TestShouldLaunchTUI_WithExplicitFlag(t *testing.T) {
	if !shouldLaunchTUI(true, []string{"ignored"}) {
		t.Fatalf("shouldLaunchTUI(true, args) = false, want true")
	}
}

func TestShouldLaunchTUI_WithArgsAndNoFlag(t *testing.T) {
	if shouldLaunchTUI(false, []string{"list"}) {
		t.Fatalf("shouldLaunchTUI(false, args) = true, want false")
	}
}
