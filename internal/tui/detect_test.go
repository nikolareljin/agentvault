package tui

import (
	"os"
	"testing"
)

// The real agent scan, against whatever is installed here. It runs on a
// developer machine only: CI runners have none of these agents, and every
// other test stubs detectAgents (see TestMain).
func TestDetectAgentsOnThisMachine(t *testing.T) {
	if os.Getenv("CI") != "" {
		t.Skip("CI is set: runners have no agents installed")
	}
	seen := map[string]bool{}
	for _, d := range detectAgentsForTUI() {
		if seen[d.Name] {
			t.Errorf("%s detected twice", d.Name)
		}
		seen[d.Name] = true
		// Version is not checked: an agent may print nothing for --version.
		if d.Status != "installed" || d.Provider == "" {
			t.Errorf("%s: incomplete entry %+v", d.Name, d)
		}
		if _, err := os.Stat(d.Path); err != nil {
			t.Errorf("%s: path %q: %v", d.Name, d.Path, err)
		}
	}
	t.Logf("detected %d agent(s)", len(seen))
}

// initialModel and refresh scan through detectAgents, so the stub in TestMain
// covers them. A direct detectAgentsForTUI call would bring back the slow,
// machine-dependent scan in every test.
func TestModelScansThroughDetectAgents(t *testing.T) {
	saved := detectAgents
	t.Cleanup(func() { detectAgents = saved })
	calls := 0
	detectAgents = func() []DetectedAgentInfo {
		calls++
		return []DetectedAgentInfo{{Name: "stubbed", Provider: "custom", Path: "/opt/stubbed", Status: "installed"}}
	}
	m := initialModel(testVault(t))
	if calls != 1 || len(m.detected) != 1 || m.detected[0].Name != "stubbed" {
		t.Fatalf("initialModel: calls=%d detected=%+v", calls, m.detected)
	}
	m.refresh()
	if calls != 2 {
		t.Fatalf("refresh: calls=%d, want 2", calls)
	}
}
