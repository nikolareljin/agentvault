package tui

import "testing"

func TestDefaultMaskURLHidesUserinfo(t *testing.T) {
	if got := MaskURL("https://user:pw@host/v1"); got != "<secret>" {
		t.Errorf("MaskURL kept userinfo: %q", got)
	}
	if got := MaskURL("http://localhost:11434"); got != "http://localhost:11434" {
		t.Errorf("MaskURL changed a plain URL: %q", got)
	}
}
