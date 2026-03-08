package agent

import "testing"

func TestGenerateSessionIDUnique(t *testing.T) {
	firstSessionID := GenerateSessionID()
	secondSessionID := GenerateSessionID()
	if firstSessionID == secondSessionID {
		t.Fatalf("GenerateSessionID() produced duplicate IDs: %q", firstSessionID)
	}
}
