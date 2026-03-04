package cmd

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

type testPromptSessionStore struct {
	shared agent.SharedConfig
}

func (s *testPromptSessionStore) SharedConfig() agent.SharedConfig {
	return s.shared
}

func (s *testPromptSessionStore) SetSharedConfig(sc agent.SharedConfig) error {
	s.shared = sc
	return nil
}

func TestPersistPromptSession_AppendsSession(t *testing.T) {
	store := &testPromptSessionStore{}
	session := agent.PromptSession{ID: "session-1", AgentName: "codex", StartedAt: time.Now(), EndedAt: time.Now()}
	if err := persistPromptSession(store, session); err != nil {
		t.Fatalf("persistPromptSession() error = %v", err)
	}
	if len(store.shared.PromptSessions) != 1 {
		t.Fatalf("len(PromptSessions) = %d, want 1", len(store.shared.PromptSessions))
	}
	if store.shared.PromptSessions[0].ID != "session-1" {
		t.Fatalf("stored session ID = %q, want session-1", store.shared.PromptSessions[0].ID)
	}
}

func TestPersistPromptSession_EnforcesRetentionLimit(t *testing.T) {
	store := &testPromptSessionStore{}
	for i := 1; i <= maxStoredPromptSessions+2; i++ {
		session := agent.PromptSession{ID: fmt.Sprintf("session-%d", i), AgentName: "codex", StartedAt: time.Now(), EndedAt: time.Now()}
		if err := persistPromptSession(store, session); err != nil {
			t.Fatalf("persistPromptSession() error = %v", err)
		}
	}
	if len(store.shared.PromptSessions) != maxStoredPromptSessions {
		t.Fatalf("len(PromptSessions) = %d, want %d", len(store.shared.PromptSessions), maxStoredPromptSessions)
	}
	if got := store.shared.PromptSessions[0].ID; got != "session-3" {
		t.Fatalf("oldest kept session = %q, want session-3", got)
	}
}

func TestToPromptTranscriptEntry_MapsFields(t *testing.T) {
	ts := time.Now().UTC()
	record := PromptRecord{
		Timestamp:       ts,
		OriginalPrompt:  "hello",
		EffectivePrompt: "optimized hello",
		ResponsePreview: "world",
		TokenUsage: PromptTokenUsage{
			InputTokens:  3,
			OutputTokens: 5,
			TotalTokens:  8,
		},
		Success: true,
	}
	entry := toPromptTranscriptEntry(record)
	if entry.Timestamp != ts {
		t.Fatalf("entry timestamp mismatch")
	}
	if entry.Prompt != "hello" || entry.EffectivePrompt != "optimized hello" {
		t.Fatalf("entry prompt mapping mismatch: %#v", entry)
	}
	if entry.TokenUsage.TotalTokens != 8 {
		t.Fatalf("entry total tokens = %d, want 8", entry.TokenUsage.TotalTokens)
	}
}

func TestToPromptTranscriptEntry_TruncatesLargePrompts(t *testing.T) {
	long := strings.Repeat("x", maxStoredPromptFieldLenInVault+100)
	record := PromptRecord{
		OriginalPrompt:  long,
		EffectivePrompt: long,
	}
	entry := toPromptTranscriptEntry(record)
	if len(entry.Prompt) != maxStoredPromptFieldLenInVault {
		t.Fatalf("len(entry.Prompt) = %d, want %d", len(entry.Prompt), maxStoredPromptFieldLenInVault)
	}
	if !strings.HasSuffix(entry.Prompt, "...") {
		t.Fatalf("entry.Prompt should end with ellipsis")
	}
	if len(entry.EffectivePrompt) != maxStoredPromptFieldLenInVault {
		t.Fatalf("len(entry.EffectivePrompt) = %d, want %d", len(entry.EffectivePrompt), maxStoredPromptFieldLenInVault)
	}
}

func TestPersistPromptSession_EnforcesEntryLimit(t *testing.T) {
	store := &testPromptSessionStore{}
	entries := make([]agent.PromptTranscriptEntry, maxEntriesPerPromptSession+10)
	for i := range entries {
		entries[i] = agent.PromptTranscriptEntry{Prompt: fmt.Sprintf("p-%d", i)}
	}
	session := agent.PromptSession{
		ID:        "session-with-many-entries",
		AgentName: "codex",
		StartedAt: time.Now(),
		EndedAt:   time.Now(),
		Entries:   entries,
	}
	if err := persistPromptSession(store, session); err != nil {
		t.Fatalf("persistPromptSession() error = %v", err)
	}
	got := store.shared.PromptSessions[0].Entries
	if len(got) != maxEntriesPerPromptSession {
		t.Fatalf("len(entries) = %d, want %d", len(got), maxEntriesPerPromptSession)
	}
	if got[0].Prompt != "p-10" {
		t.Fatalf("oldest kept prompt = %q, want p-10", got[0].Prompt)
	}
}
