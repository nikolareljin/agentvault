package cmd

import (
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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

func TestPersistPromptSession_EnforcesRetentionByRecency(t *testing.T) {
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	sessions := make([]agent.PromptSession, 0, maxStoredPromptSessions)
	// Intentionally place a very recent session first to simulate out-of-order imports.
	sessions = append(sessions, agent.PromptSession{
		ID:        fmt.Sprintf("session-%d", maxStoredPromptSessions),
		AgentName: "codex",
		StartedAt: base.Add(time.Duration(maxStoredPromptSessions) * time.Minute),
		EndedAt:   base.Add(time.Duration(maxStoredPromptSessions) * time.Minute),
	})
	for i := 1; i < maxStoredPromptSessions; i++ {
		sessions = append(sessions, agent.PromptSession{
			ID:        fmt.Sprintf("session-%d", i),
			AgentName: "codex",
			StartedAt: base.Add(time.Duration(i) * time.Minute),
			EndedAt:   base.Add(time.Duration(i) * time.Minute),
		})
	}
	store := &testPromptSessionStore{
		shared: agent.SharedConfig{
			PromptSessions: sessions,
		},
	}

	newestID := fmt.Sprintf("session-%d", maxStoredPromptSessions+1)
	if err := persistPromptSession(store, agent.PromptSession{
		ID:        newestID,
		AgentName: "codex",
		StartedAt: base.Add(time.Duration(maxStoredPromptSessions+1) * time.Minute),
		EndedAt:   base.Add(time.Duration(maxStoredPromptSessions+1) * time.Minute),
	}); err != nil {
		t.Fatalf("persistPromptSession() error = %v", err)
	}

	if len(store.shared.PromptSessions) != maxStoredPromptSessions {
		t.Fatalf("len(PromptSessions) = %d, want %d", len(store.shared.PromptSessions), maxStoredPromptSessions)
	}

	kept := make(map[string]struct{}, len(store.shared.PromptSessions))
	for _, s := range store.shared.PromptSessions {
		kept[s.ID] = struct{}{}
	}
	if _, ok := kept["session-1"]; ok {
		t.Fatalf("session-1 should be evicted as the oldest session")
	}
	if _, ok := kept[fmt.Sprintf("session-%d", maxStoredPromptSessions)]; !ok {
		t.Fatalf("most recent pre-existing session should be retained")
	}
	if _, ok := kept[newestID]; !ok {
		t.Fatalf("newly appended most-recent session should be retained")
	}
}

func TestToPromptTranscriptEntry_MapsFields(t *testing.T) {
	ts := time.Now().UTC()
	record := PromptRecord{
		Timestamp:       ts,
		OriginalPrompt:  "hello",
		EffectivePrompt: "optimized hello",
		ResponsePreview: "world",
		TokenUsage: &agent.PromptTokenUsage{
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
	if entry.TokenUsage == nil || entry.TokenUsage.TotalTokens != 8 {
		t.Fatalf("entry total tokens = %#v, want 8", entry.TokenUsage)
	}
}

func TestToPromptTranscriptEntry_OmitsZeroTokenUsage(t *testing.T) {
	entry := toPromptTranscriptEntry(PromptRecord{
		OriginalPrompt: "hello",
	})
	if entry.TokenUsage != nil {
		t.Fatalf("expected nil token usage for zero values, got %#v", entry.TokenUsage)
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

func TestToPromptTranscriptEntry_TruncatesOnRuneBoundary(t *testing.T) {
	// Use a multi-byte rune to ensure truncation does not split UTF-8 bytes.
	long := strings.Repeat("界", maxStoredPromptFieldLenInVault+20)
	entry := toPromptTranscriptEntry(PromptRecord{
		OriginalPrompt: long,
	})
	if !utf8.ValidString(entry.Prompt) {
		t.Fatalf("truncated prompt is not valid UTF-8")
	}
	if len([]rune(entry.Prompt)) != maxStoredPromptFieldLenInVault {
		t.Fatalf("rune length = %d, want %d", len([]rune(entry.Prompt)), maxStoredPromptFieldLenInVault)
	}
	if !strings.HasSuffix(entry.Prompt, "...") {
		t.Fatalf("entry.Prompt should end with ellipsis")
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

func TestAppendPromptSessionEntryWithCap_EnforcesInMemoryLimit(t *testing.T) {
	session := agent.PromptSession{}
	for i := 0; i < maxEntriesPerPromptSession+10; i++ {
		appendPromptSessionEntryWithCap(&session, agent.PromptTranscriptEntry{Prompt: fmt.Sprintf("p-%d", i)})
	}
	if len(session.Entries) != maxEntriesPerPromptSession {
		t.Fatalf("len(entries) = %d, want %d", len(session.Entries), maxEntriesPerPromptSession)
	}
	if session.Entries[0].Prompt != "p-10" {
		t.Fatalf("oldest kept prompt = %q, want p-10", session.Entries[0].Prompt)
	}
}

func TestGeneratePromptModeSessionID_SkipsEmptyAndDuplicate(t *testing.T) {
	originalGenerator := promptModeSessionIDGenerator
	t.Cleanup(func() {
		promptModeSessionIDGenerator = originalGenerator
	})

	sequence := []string{"", "dup", "fresh"}
	var i int
	promptModeSessionIDGenerator = func() string {
		if i >= len(sequence) {
			return "fresh"
		}
		value := sequence[i]
		i++
		return value
	}

	id := generatePromptModeSessionID([]agent.PromptSession{
		{ID: "prompt-session-dup"},
	})
	if id != "prompt-session-fresh" {
		t.Fatalf("generatePromptModeSessionID() = %q, want prompt-session-fresh", id)
	}
}
