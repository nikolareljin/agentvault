package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
)

// PromptRecord stores one agentvault prompt-through execution entry.
type PromptRecord struct {
	ID                  string                  `json:"id"`
	Timestamp           time.Time               `json:"timestamp"`
	AgentName           string                  `json:"agent_name"`
	Provider            string                  `json:"provider"`
	Model               string                  `json:"model,omitempty"`
	Optimized           bool                    `json:"optimized"`
	OptimizationProfile string                  `json:"optimization_profile,omitempty"`
	OriginalPrompt      string                  `json:"original_prompt"`
	EffectivePrompt     string                  `json:"effective_prompt"`
	TokenUsage          *agent.PromptTokenUsage `json:"token_usage,omitempty"`
	ResponsePreview     string                  `json:"response_preview,omitempty"`
	Success             bool                    `json:"success"`
	Error               string                  `json:"error,omitempty"`
}

type promptResult struct {
	Response string
	Usage    agent.PromptTokenUsage
}

var promptCmd = &cobra.Command{
	Use:   "prompt [agent-name]",
	Short: "Send prompts through AgentVault gateway to a configured local agent",
	Long: `Route a prompt through AgentVault instead of calling provider CLIs directly.

This gives AgentVault orchestration visibility over:
  - what prompt was sent (original and effective)
  - token usage for the request when available
  - provider/model actually used

For local Ollama agents, AgentVault can auto-optimize prompts for clearer
instructions to reduce wasted tokens on clarification. This optimization also
supports codex/copilot-style coding flows and a generic profile for other agents.

Examples:
  agentvault prompt my-codex --text "review this diff"
  agentvault prompt my-ollama --text "build a parser" --json
  cat prompt.txt | agentvault prompt my-ollama --optimize-ollama`,
	Args: cobra.ExactArgs(1),
	RunE: runPrompt,
}

func init() {
	rootCmd.AddCommand(promptCmd)
	promptCmd.Flags().String("text", "", "prompt text")
	promptCmd.Flags().String("file", "", "read prompt text from file")
	promptCmd.Flags().Bool("json", false, "output machine-readable JSON")
	promptCmd.Flags().Bool("optimize", true, "rewrite/structure prompt for better execution efficiency")
	promptCmd.Flags().String("optimize-profile", "auto", "optimization profile: auto|generic|ollama|codex|copilot|claude")
	promptCmd.Flags().Bool("optimize-ollama", true, "deprecated: kept for compatibility; use --optimize/--optimize-profile")
	promptCmd.Flags().Bool("dry-run", false, "show effective prompt without executing")
	promptCmd.Flags().Bool("no-log", false, "do not write prompt execution history")
	promptCmd.Flags().String("history-file", "", "history file path (default: ~/.config/agentvault/prompt-history.jsonl)")
	promptCmd.Flags().Duration("timeout", 5*time.Minute, "provider call timeout")
}

func runPrompt(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	a, ok := v.Get(args[0])
	if !ok {
		return fmt.Errorf("agent %q not found", args[0])
	}

	text, err := readPromptInput(cmd)
	if err != nil {
		return err
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("prompt is empty")
	}

	shared := v.SharedConfig()
	effectivePrompt := text
	optimizeEnabled, _ := cmd.Flags().GetBool("optimize")
	optimizeOllamaCompat, _ := cmd.Flags().GetBool("optimize-ollama")
	profileFlag, _ := cmd.Flags().GetString("optimize-profile")
	if !optimizeOllamaCompat {
		optimizeEnabled = false
	}
	optimized := false
	optimizationProfile := ""
	if optimizeEnabled {
		effectivePrompt, optimizationProfile = optimizePromptForAgent(text, a, shared, profileFlag)
		optimized = true
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	jsonOut, _ := cmd.Flags().GetBool("json")
	if dryRun {
		if jsonOut {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"agent":            a.Name,
				"provider":         a.Provider,
				"optimized":        optimized,
				"profile":          optimizationProfile,
				"effective_prompt": effectivePrompt,
			})
			return nil
		}
		fmt.Println(effectivePrompt)
		return nil
	}

	timeout, _ := cmd.Flags().GetDuration("timeout")
	result, execErr := executePrompt(a, effectivePrompt, timeout)

	record := PromptRecord{
		ID:                  fmt.Sprintf("prompt-%d", time.Now().UnixNano()),
		Timestamp:           time.Now().UTC(),
		AgentName:           a.Name,
		Provider:            string(a.Provider),
		Model:               a.Model,
		Optimized:           optimized,
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

	noLog, _ := cmd.Flags().GetBool("no-log")
	historyPath, _ := cmd.Flags().GetString("history-file")
	if historyPath == "" {
		historyPath = resolvePromptHistoryPath()
	}
	if !noLog {
		if err := appendPromptRecord(historyPath, record); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write prompt history: %v\n", err)
		}
	}

	if execErr != nil {
		if jsonOut {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"record": record,
				"error":  execErr.Error(),
			})
			return nil
		}
		return execErr
	}

	if jsonOut {
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"record":   record,
			"response": result.Response,
		})
		return nil
	}

	fmt.Println(result.Response)
	if usage := optionalTokenUsage(result.Usage); usage != nil {
		fmt.Fprintf(os.Stderr, "tokens used: input=%d output=%d total=%d\n",
			usage.InputTokens, usage.OutputTokens, usage.TotalTokens)
	}
	return nil
}

func optionalTokenUsage(usage agent.PromptTokenUsage) *agent.PromptTokenUsage {
	if usage.InputTokens == 0 &&
		usage.CachedInputTokens == 0 &&
		usage.OutputTokens == 0 &&
		usage.ReasoningOutputTokens == 0 &&
		usage.TotalTokens == 0 {
		return nil
	}
	u := usage
	return &u
}

func readPromptInput(cmd *cobra.Command) (string, error) {
	text, _ := cmd.Flags().GetString("text")
	file, _ := cmd.Flags().GetString("file")

	if text != "" && file != "" {
		return "", errors.New("use either --text or --file, not both")
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("reading prompt file: %w", err)
		}
		return string(data), nil
	}
	if text != "" {
		return text, nil
	}

	info, err := os.Stdin.Stat()
	if err == nil && (info.Mode()&os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("reading stdin prompt: %w", err)
		}
		return string(data), nil
	}

	return "", errors.New("no prompt provided; use --text, --file, or stdin")
}

func executePrompt(a agent.Agent, prompt string, timeout time.Duration) (promptResult, error) {
	switch a.Provider {
	case agent.ProviderOllama:
		return executeOllamaPrompt(a, prompt, timeout)
	case agent.ProviderCodex:
		return executeCodexPrompt(a, prompt, timeout)
	case agent.ProviderClaude:
		return executeClaudePrompt(a, prompt, timeout)
	default:
		return promptResult{}, fmt.Errorf("provider %q is not supported by prompt gateway yet", a.Provider)
	}
}

func executeOllamaPrompt(a agent.Agent, prompt string, timeout time.Duration) (promptResult, error) {
	if strings.TrimSpace(a.Model) == "" {
		return promptResult{}, errors.New("ollama agent requires model")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(a.BaseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	payload := map[string]any{
		"model":  a.Model,
		"prompt": prompt,
		"stream": false,
	}
	body, _ := json.Marshal(payload)

	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodPost, baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return promptResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return promptResult{}, fmt.Errorf("calling ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return promptResult{}, fmt.Errorf("ollama error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var out struct {
		Response        string `json:"response"`
		PromptEvalCount int64  `json:"prompt_eval_count"`
		EvalCount       int64  `json:"eval_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return promptResult{}, fmt.Errorf("decoding ollama response: %w", err)
	}

	usage := agent.PromptTokenUsage{
		InputTokens:  out.PromptEvalCount,
		OutputTokens: out.EvalCount,
		TotalTokens:  out.PromptEvalCount + out.EvalCount,
	}
	return promptResult{Response: strings.TrimSpace(out.Response), Usage: usage}, nil
}

func executeCodexPrompt(a agent.Agent, prompt string, timeout time.Duration) (promptResult, error) {
	if _, err := exec.LookPath("codex"); err != nil {
		return promptResult{}, errors.New("codex binary not found in PATH")
	}

	tmp, err := os.CreateTemp("", "agentvault-codex-last-*.txt")
	if err != nil {
		return promptResult{}, err
	}
	_ = tmp.Close()
	defer os.Remove(tmp.Name())

	args := []string{"exec", "--json", "--output-last-message", tmp.Name()}
	if strings.TrimSpace(a.Model) != "" {
		args = append(args, "--model", a.Model)
	}
	args = append(args, prompt)

	runCtx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(context.Background(), timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, "codex", args...)
	cmd.Env = os.Environ()
	if strings.TrimSpace(a.APIKey) != "" {
		cmd.Env = append(cmd.Env, "OPENAI_API_KEY="+a.APIKey)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return promptResult{}, fmt.Errorf("codex exec timed out after %s", timeout)
		}
		return promptResult{}, fmt.Errorf("codex exec failed: %v (%s)", err, strings.TrimSpace(stderr.String()))
	}

	usage := parseCodexUsage(stdout.String())
	respBytes, _ := os.ReadFile(tmp.Name())
	response := strings.TrimSpace(string(respBytes))
	if response == "" {
		response = strings.TrimSpace(stdout.String())
	}

	return promptResult{Response: response, Usage: usage}, nil
}

func parseCodexUsage(raw string) agent.PromptTokenUsage {
	usage := agent.PromptTokenUsage{}
	type evt struct {
		Payload struct {
			Type string `json:"type"`
			Info struct {
				TotalTokenUsage struct {
					InputTokens           int64 `json:"input_tokens"`
					CachedInputTokens     int64 `json:"cached_input_tokens"`
					OutputTokens          int64 `json:"output_tokens"`
					ReasoningOutputTokens int64 `json:"reasoning_output_tokens"`
					TotalTokens           int64 `json:"total_tokens"`
				} `json:"total_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}

	s := bufio.NewScanner(strings.NewReader(raw))
	// Token-count JSON events may exceed Scanner's default 64K token limit.
	s.Buffer(make([]byte, 64*1024), 2*1024*1024)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var e evt
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		if e.Payload.Type != "token_count" {
			continue
		}
		usage = agent.PromptTokenUsage{
			InputTokens:           e.Payload.Info.TotalTokenUsage.InputTokens,
			CachedInputTokens:     e.Payload.Info.TotalTokenUsage.CachedInputTokens,
			OutputTokens:          e.Payload.Info.TotalTokenUsage.OutputTokens,
			ReasoningOutputTokens: e.Payload.Info.TotalTokenUsage.ReasoningOutputTokens,
			TotalTokens:           e.Payload.Info.TotalTokenUsage.TotalTokens,
		}
	}
	if err := s.Err(); err != nil {
		return usage
	}
	return usage
}

func executeClaudePrompt(a agent.Agent, prompt string, timeout time.Duration) (promptResult, error) {
	if _, err := exec.LookPath("claude"); err != nil {
		return promptResult{}, errors.New("claude binary not found in PATH")
	}

	args := []string{"-p", "--output-format", "json"}
	if strings.TrimSpace(a.Model) != "" {
		args = append(args, "--model", a.Model)
	}
	args = append(args, prompt)

	runCtx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(context.Background(), timeout)
	}
	defer cancel()

	cmd := exec.CommandContext(runCtx, "claude", args...)
	cmd.Env = os.Environ()
	if strings.TrimSpace(a.APIKey) != "" {
		cmd.Env = append(cmd.Env, "ANTHROPIC_API_KEY="+a.APIKey)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return promptResult{}, fmt.Errorf("claude timed out after %s", timeout)
		}
		return promptResult{}, fmt.Errorf("claude failed: %v (%s)", err, strings.TrimSpace(stderr.String()))
	}

	raw := strings.TrimSpace(stdout.String())
	if raw == "" {
		return promptResult{}, errors.New("claude returned empty output")
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		// Claude may still output plain text depending on version/config.
		return promptResult{Response: raw}, nil
	}

	response := extractString(decoded, []string{"result", "response", "output", "content", "text"})
	if response == "" {
		response = raw
	}

	usage := agent.PromptTokenUsage{
		InputTokens:  extractInt64(decoded, []string{"input_tokens", "prompt_tokens", "usage.input_tokens", "usage.prompt_tokens"}),
		OutputTokens: extractInt64(decoded, []string{"output_tokens", "completion_tokens", "usage.output_tokens", "usage.completion_tokens"}),
		TotalTokens:  extractInt64(decoded, []string{"total_tokens", "usage.total_tokens"}),
	}
	if usage.TotalTokens == 0 && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}

	return promptResult{Response: strings.TrimSpace(response), Usage: usage}, nil
}

func appendPromptRecord(path string, rec PromptRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	return enc.Encode(rec)
}

func truncateForHistory(s string) string {
	trimmed := strings.TrimSpace(s)
	runes := []rune(trimmed)
	if len(runes) <= 500 {
		return trimmed
	}
	return string(runes[:497]) + "..."
}

func optimizePromptForAgent(original string, a agent.Agent, shared agent.SharedConfig, requestedProfile string) (string, string) {
	prompt := strings.TrimSpace(original)
	if prompt == "" {
		return original, "none"
	}

	profile := chooseOptimizationProfile(a, requestedProfile)
	roleTitle := a.Role
	if role, ok := agent.GetRole(shared.Roles, a.Role); ok && strings.TrimSpace(role.Title) != "" {
		roleTitle = role.Title
	}
	if roleTitle == "" {
		roleTitle = "software engineer"
	}
	disabledSet := make(map[string]struct{}, len(a.DisabledRules))
	for _, name := range a.DisabledRules {
		disabledSet[name] = struct{}{}
	}
	enabledRules := make([]agent.UnifiedRule, 0, len(shared.Rules))
	for _, r := range shared.Rules {
		if !r.Enabled {
			continue
		}
		if _, disabled := disabledSet[r.Name]; disabled {
			continue
		}
		enabledRules = append(enabledRules, r)
	}
	sort.SliceStable(enabledRules, func(i, j int) bool {
		return enabledRules[i].Priority < enabledRules[j].Priority
	})
	rules := []string{}
	for _, r := range enabledRules {
		rules = append(rules, "- "+r.Content)
	}

	var b strings.Builder
	switch profile {
	case "ollama":
		b.WriteString("You are an expert assistant. Keep responses concise and implementation-focused.\n\n")
	case "codex", "copilot":
		b.WriteString("You are a senior coding agent. Prioritize correctness, minimal diffs, and runnable outputs.\n\n")
	case "claude":
		b.WriteString("You are a careful engineering assistant. Explain assumptions briefly and provide precise changes.\n\n")
	default:
		b.WriteString("You are an expert assistant. Respond with concise, actionable output.\n\n")
	}
	b.WriteString("## Task\n")
	b.WriteString(prompt)
	b.WriteString("\n\n## Context\n")
	b.WriteString("- Intended role: ")
	b.WriteString(roleTitle)
	b.WriteString("\n")
	if strings.TrimSpace(a.Model) != "" {
		b.WriteString("- Model: ")
		b.WriteString(a.Model)
		b.WriteString("\n")
	}
	if len(rules) > 0 {
		b.WriteString("\n## Constraints\n")
		for i, r := range rules {
			if i >= 8 {
				break
			}
			b.WriteString(r)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n## Output format\n")
	b.WriteString("1. Short answer first.\n")
	b.WriteString("2. Concrete steps/changes next.\n")
	b.WriteString("3. Call out assumptions and risks.\n")

	return b.String(), profile
}

func chooseOptimizationProfile(a agent.Agent, requested string) string {
	switch strings.ToLower(strings.TrimSpace(requested)) {
	case "generic", "ollama", "codex", "copilot", "claude":
		return strings.ToLower(strings.TrimSpace(requested))
	case "", "auto":
	default:
		return "generic"
	}

	if a.Provider == agent.ProviderOllama {
		return "ollama"
	}
	if a.Provider == agent.ProviderCodex || a.Provider == agent.ProviderAider || a.Provider == agent.ProviderMeldbot || a.Provider == agent.ProviderOpenclaw || a.Provider == agent.ProviderNanoclaw {
		return "codex"
	}
	if a.Provider == agent.ProviderClaude {
		return "claude"
	}
	// Heuristic for custom/copilot-like agents.
	name := strings.ToLower(a.Name + " " + a.Model)
	if strings.Contains(name, "copilot") {
		return "copilot"
	}
	return "generic"
}

func extractString(data map[string]any, paths []string) string {
	for _, p := range paths {
		if v, ok := lookupPath(data, p); ok {
			s, ok := v.(string)
			if ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

func extractInt64(data map[string]any, paths []string) int64 {
	for _, p := range paths {
		if v, ok := lookupPath(data, p); ok {
			switch n := v.(type) {
			case float64:
				return int64(n)
			case int64:
				return n
			case int:
				return int64(n)
			case json.Number:
				i, _ := n.Int64()
				return i
			}
		}
	}
	return 0
}

func lookupPath(data map[string]any, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any = data
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		val, ok := m[p]
		if !ok {
			return nil, false
		}
		cur = val
	}
	return cur, true
}
