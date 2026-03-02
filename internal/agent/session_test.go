package agent

import "testing"

func TestGenerateSessionIDUnique(t *testing.T) {
	id1 := GenerateSessionID()
	id2 := GenerateSessionID()
	if id1 == id2 {
		t.Fatalf("GenerateSessionID() produced duplicate IDs: %q", id1)
	}
}
