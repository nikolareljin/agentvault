package cmd

import "testing"

func TestShouldLaunchTUI_DefaultNoArgs(t *testing.T) {
	if !shouldLaunchTUI(false, false, nil) {
		t.Fatalf("shouldLaunchTUI(false, false, nil) = false, want true")
	}
}

func TestShouldLaunchTUI_WithExplicitFlag(t *testing.T) {
	if !shouldLaunchTUI(true, true, []string{"ignored"}) {
		t.Fatalf("shouldLaunchTUI(true, true, args) = false, want true")
	}
}

func TestShouldLaunchTUI_WithArgsAndNoFlag(t *testing.T) {
	if shouldLaunchTUI(false, false, []string{"list"}) {
		t.Fatalf("shouldLaunchTUI(false, false, args) = true, want false")
	}
}

func TestShouldLaunchTUI_ExplicitFalseDisablesDefault(t *testing.T) {
	if shouldLaunchTUI(true, false, nil) {
		t.Fatalf("shouldLaunchTUI(true, false, nil) = true, want false")
	}
}
