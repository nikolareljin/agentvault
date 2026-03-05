package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
)

const (
	maxStoredPromptSessions        = agent.PromptSessionRetentionLimit
	maxEntriesPerPromptSession     = agent.PromptSessionEntryLimit
	maxStoredPromptFieldLenInVault = agent.PromptTranscriptFieldMaxRunes
)

var (
	promptModeInput              io.Reader = os.Stdin
	promptModeOutput             io.Writer = os.Stdout
	promptModeErr                io.Writer = os.Stderr
	promptModeSessionIDGenerator           = agent.GenerateSessionID
)

func runPromptMode() error {
	v, err := openVault()
	if err != nil {
		return err
	}

	agents := v.List()
	if len(agents) == 0 {
		return fmt.Errorf("no agents configured; add one first with 'agentvault add <name> --provider <provider>'")
	}

	reader := bufio.NewReader(promptModeInput)
	selected, canceled, err := selectPromptModeAgent(reader, agents)
	if err != nil {
		return err
	}
	if canceled {
		fmt.Fprintln(promptModeOutput, "Prompt mode canceled.")
		return nil
	}

	storeSession, err := askYesNo(reader, "Store this prompt transcript in vault state on exit? [y/N]: ")
	if err != nil {
		return err
	}
	logHistory, err := shouldLogPromptModeHistory(reader)
	if err != nil {
		return err
	}

	session := agent.PromptSession{
		ID:        generatePromptModeSessionID(v.SharedConfig().PromptSessions),
		AgentName: selected.Name,
		Provider:  string(selected.Provider),
		Model:     selected.Model,
		StartedAt: time.Now().UTC(),
	}

	fmt.Fprintf(promptModeOutput, "Entering prompt mode with agent %q (%s).\n", selected.Name, selected.Provider)
	fmt.Fprintln(promptModeOutput, "Type a prompt to submit, '/cancel' to skip the current input, and '/exit' to leave.")

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	historyPath := resolvePromptHistoryPath()
	for {
		fmt.Fprint(promptModeOutput, "prompt> ")
		if !scanner.Scan() {
			fmt.Fprintln(promptModeOutput)
			break
		}

		input := strings.TrimSpace(scanner.Text())
		switch strings.ToLower(input) {
		case "", "/cancel", "cancel":
			if input != "" {
				fmt.Fprintln(promptModeOutput, "Canceled.")
			}
			continue
		case "/exit", "exit", "quit", ":q":
			goto done
		}

		record, response, execErr := executePromptInteraction(selected, v.SharedConfig(), input, 5*time.Minute)
		appendPromptSessionEntryWithCap(&session, toPromptTranscriptEntry(record))

		if logHistory {
			if err := appendPromptRecord(historyPath, record); err != nil {
				fmt.Fprintf(promptModeErr, "warning: could not write prompt history: %v\n", err)
			}
		}

		if execErr != nil {
			fmt.Fprintf(promptModeErr, "error: %v\n", execErr)
			continue
		}

		fmt.Fprintln(promptModeOutput, response)
		if record.TokenUsage != nil {
			fmt.Fprintf(promptModeErr, "tokens used: input=%d output=%d total=%d\n",
				record.TokenUsage.InputTokens,
				record.TokenUsage.OutputTokens,
				record.TokenUsage.TotalTokens,
			)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("reading prompt input: %w", err)
	}

done:
	session.EndedAt = time.Now().UTC()
	if !storeSession {
		fmt.Fprintln(promptModeOutput, "Prompt mode ended. Transcript was not saved to vault state.")
		return nil
	}
	if len(session.Entries) == 0 {
		fmt.Fprintln(promptModeOutput, "Prompt mode ended. No submitted prompts to save.")
		return nil
	}

	if err := persistPromptSession(v, session); err != nil {
		return err
	}
	fmt.Fprintf(promptModeOutput, "Saved prompt transcript session %q to vault state.\n", session.ID)
	return nil
}

func appendPromptSessionEntryWithCap(session *agent.PromptSession, entry agent.PromptTranscriptEntry) {
	session.Entries = append(session.Entries, entry)
	if len(session.Entries) > maxEntriesPerPromptSession {
		session.Entries = session.Entries[len(session.Entries)-maxEntriesPerPromptSession:]
	}
}

func generatePromptModeSessionID(existing []agent.PromptSession) string {
	seen := make(map[string]struct{}, len(existing))
	for _, session := range existing {
		if session.ID == "" {
			continue
		}
		seen[session.ID] = struct{}{}
	}

	for {
		baseID := strings.TrimSpace(promptModeSessionIDGenerator())
		if baseID == "" {
			continue
		}
		candidate := fmt.Sprintf("prompt-session-%s", baseID)
		if _, exists := seen[candidate]; exists {
			continue
		}
		return candidate
	}
}

func selectPromptModeAgent(reader *bufio.Reader, agents []agent.Agent) (agent.Agent, bool, error) {
	if len(agents) == 1 {
		return agents[0], false, nil
	}

	fmt.Fprintln(promptModeOutput, "Select an agent for prompt mode:")
	for i, a := range agents {
		fmt.Fprintf(promptModeOutput, "  %d. %s (%s)\n", i+1, a.Name, a.Provider)
	}
	fmt.Fprint(promptModeOutput, "Enter agent number/name (blank to cancel): ")

	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return agent.Agent{}, false, fmt.Errorf("reading agent selection: %w", err)
	}
	selection := strings.TrimSpace(line)
	if selection == "" || strings.EqualFold(selection, "cancel") {
		return agent.Agent{}, true, nil
	}

	for i, a := range agents {
		if fmt.Sprintf("%d", i+1) == selection || strings.EqualFold(a.Name, selection) {
			return a, false, nil
		}
	}
	return agent.Agent{}, false, fmt.Errorf("unknown agent selection %q", selection)
}

func askYesNo(reader *bufio.Reader, prompt string) (bool, error) {
	fmt.Fprint(promptModeOutput, prompt)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return false, fmt.Errorf("reading yes/no input: %w", err)
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func shouldLogPromptModeHistory(reader *bufio.Reader) (bool, error) {
	return askYesNo(reader, "Write plaintext prompt history to disk (equivalent to prompt command logging)? [y/N]: ")
}

func executePromptInteraction(a agent.Agent, shared agent.SharedConfig, text string, timeout time.Duration) (PromptRecord, string, error) {
	effectivePrompt, optimizationProfile := optimizePromptForAgent(text, a, shared, "auto")
	result, execErr := executePrompt(a, effectivePrompt, timeout)

	record := PromptRecord{
		ID:                  fmt.Sprintf("prompt-%d", time.Now().UnixNano()),
		Timestamp:           time.Now().UTC(),
		AgentName:           a.Name,
		Provider:            string(a.Provider),
		Model:               a.Model,
		Optimized:           true,
		OptimizationProfile: optimizationProfile,
		OriginalPrompt:      text,
		EffectivePrompt:     effectivePrompt,
		Success:             execErr == nil,
	}
	if execErr == nil {
		record.TokenUsage = optionalTokenUsage(result.Usage)
		record.ResponsePreview = truncateForHistory(result.Response)
	} else {
		record.Error = execErr.Error()
	}

	return record, result.Response, execErr
}

func toPromptTranscriptEntry(record PromptRecord) agent.PromptTranscriptEntry {
	entry := agent.PromptTranscriptEntry{
		Timestamp:       record.Timestamp,
		Prompt:          truncatePromptFieldForVault(record.OriginalPrompt),
		EffectivePrompt: truncatePromptFieldForVault(record.EffectivePrompt),
		ResponsePreview: truncatePromptFieldForVault(record.ResponsePreview),
		Success:         record.Success,
		Error:           truncatePromptFieldForVault(record.Error),
	}
	if record.TokenUsage != nil {
		usage := agent.PromptTokenUsage{
			InputTokens:           record.TokenUsage.InputTokens,
			CachedInputTokens:     record.TokenUsage.CachedInputTokens,
			OutputTokens:          record.TokenUsage.OutputTokens,
			ReasoningOutputTokens: record.TokenUsage.ReasoningOutputTokens,
			TotalTokens:           record.TokenUsage.TotalTokens,
		}
		if usage.InputTokens > 0 || usage.CachedInputTokens > 0 || usage.OutputTokens > 0 || usage.ReasoningOutputTokens > 0 || usage.TotalTokens > 0 {
			entry.TokenUsage = &usage
		}
	}
	return entry
}

type promptSessionStore interface {
	SharedConfig() agent.SharedConfig
	SetSharedConfig(agent.SharedConfig) error
}

func persistPromptSession(store promptSessionStore, session agent.PromptSession) error {
	if len(session.Entries) > maxEntriesPerPromptSession {
		// Cap entries per session to keep encrypted vault growth predictable.
		start := len(session.Entries) - maxEntriesPerPromptSession
		capped := make([]agent.PromptTranscriptEntry, maxEntriesPerPromptSession)
		copy(capped, session.Entries[start:])
		session.Entries = capped
	}
	sc := store.SharedConfig()
	sc.PromptSessions = append(sc.PromptSessions, session)
	sort.SliceStable(sc.PromptSessions, func(i, j int) bool {
		return promptSessionRecencyTimestamp(sc.PromptSessions[i]).Before(promptSessionRecencyTimestamp(sc.PromptSessions[j]))
	})
	if len(sc.PromptSessions) > maxStoredPromptSessions {
		// Keep only the most recent sessions to avoid unbounded vault growth.
		start := len(sc.PromptSessions) - maxStoredPromptSessions
		capped := make([]agent.PromptSession, maxStoredPromptSessions)
		copy(capped, sc.PromptSessions[start:])
		sc.PromptSessions = capped
	}
	return store.SetSharedConfig(sc)
}

func promptSessionRecencyTimestamp(session agent.PromptSession) time.Time {
	if !session.EndedAt.IsZero() {
		return session.EndedAt
	}
	if !session.StartedAt.IsZero() {
		return session.StartedAt
	}
	return time.Time{}
}

func truncatePromptFieldForVault(s string) string {
	trimmed := strings.TrimSpace(s)
	return truncateRunesWithEllipsis(trimmed, maxStoredPromptFieldLenInVault)
}
