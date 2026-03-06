package envutil

import "testing"

func TestSetValueWithPrecedence_ReplacesExistingKey(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"OPENAI_API_KEY=old",
		"HOME=/tmp/home",
	}
	got := SetValueWithPrecedence(base, "OPENAI_API_KEY", "new")

	var (
		count int
		value string
	)
	for _, entry := range got {
		if len(entry) >= len("OPENAI_API_KEY=") && entry[:len("OPENAI_API_KEY=")] == "OPENAI_API_KEY=" {
			count++
			value = entry[len("OPENAI_API_KEY="):]
		}
	}
	if count != 1 {
		t.Fatalf("OPENAI_API_KEY entries = %d, want 1 (%v)", count, got)
	}
	if value != "new" {
		t.Fatalf("OPENAI_API_KEY value = %q, want %q", value, "new")
	}
}

func TestSetValueWithPrecedence_RemovesKeyWhenValueEmpty(t *testing.T) {
	base := []string{"A=1", "B=2"}
	got := SetValueWithPrecedence(base, "B", "")
	for _, entry := range got {
		if entry == "B=2" || entry == "B=" {
			t.Fatalf("unexpected B entry after removal: %v", got)
		}
	}
}

func TestEnvKeyEqualsForOS_WindowsIsCaseInsensitive(t *testing.T) {
	if !envKeyEqualsForOS("windows", "OpenAI_Api_Key", "OPENAI_API_KEY") {
		t.Fatal("expected case-insensitive match for windows")
	}
}

func TestEnvKeyEqualsForOS_LinuxIsCaseSensitive(t *testing.T) {
	if envKeyEqualsForOS("linux", "OpenAI_Api_Key", "OPENAI_API_KEY") {
		t.Fatal("expected case-sensitive mismatch for linux")
	}
}
