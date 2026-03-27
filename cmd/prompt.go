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
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/nikolareljin/agentvault/internal/envutil"
	routerpkg "github.com/nikolareljin/agentvault/internal/router"
	"github.com/nikolareljin/agentvault/internal/textutil"
	"github.com/spf13/cobra"
)

// PromptRecord stores one agentvault prompt-through execution entry.
type PromptRecord struct {
	ID                  string                  `json:"id"`
	Timestamp           time.Time               `json:"timestamp"`
	AgentName           string                  `json:"agent_name"`
	Provider            string                  `json:"provider"`
	Runner              string                  `json:"runner,omitempty"`
	RouterMode          string                  `json:"router_mode,omitempty"`
	RouteClass          string                  `json:"route_class,omitempty"`
	RouteReasons        []string                `json:"route_reasons,omitempty"`
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
	Args: validatePromptArgs,
	RunE: runPrompt,
}

func init() {
	rootCmd.AddCommand(promptCmd)
	promptCmd.Flags().String("text", "", "prompt text")
	promptCmd.Flags().String("file", "", "read prompt text from file")
	promptCmd.Flags().String("workflow", "", "guided workflow prompt: implement_issue|issue|implement_pr|pr|fix_pr")
	promptCmd.Flags().String("repo", "", "repository path for workflow context (default: current directory)")
	promptCmd.Flags().String("issue", "", "issue reference for --workflow implement_issue")
	promptCmd.Flags().String("pr", "", "pull request reference for --workflow implement_pr")
	promptCmd.Flags().Bool("json", false, "output machine-readable JSON")
	promptCmd.Flags().Bool("auto", false, "route the prompt automatically instead of selecting an agent manually (defaults to local-first routing when no other preferences are set)")
	promptCmd.Flags().String("router", "", "router mode override: heuristic|langgraph")
	promptCmd.Flags().String("langgraph-cmd", "", "langgraph router script path override (or set AGENTVAULT_LANGGRAPH_ROUTER_CMD)")
	promptCmd.Flags().Bool("prefer-local", false, "prefer local execution targets during routing (effective default when no other routing preferences are set)")
	promptCmd.Flags().Bool("prefer-fast", false, "prefer lower-latency targets during routing")
	promptCmd.Flags().Bool("prefer-low-cost", false, "prefer lower-cost targets during routing")
	promptCmd.Flags().Bool("local-only", false, "restrict routing to local execution targets only")
	promptCmd.Flags().Bool("optimize", true, "rewrite/structure prompt for better execution efficiency")
	promptCmd.Flags().String("optimize-profile", "auto", "optimization profile: auto|generic|ollama|codex|copilot|claude")
	promptCmd.Flags().Bool("optimize-ollama", true, "deprecated: kept for compatibility; use --optimize/--optimize-profile")
	promptCmd.Flags().Bool("dry-run", false, "show effective prompt without executing")
	promptCmd.Flags().Bool("validate-only", false, "validate configured provider/backend connectivity and exit")
	promptCmd.Flags().Bool("no-log", false, "do not write prompt execution history")
	promptCmd.Flags().String("history-file", "", "history file path (default: ~/.config/agentvault/prompt-history.jsonl)")
	promptCmd.Flags().Duration("timeout", 5*time.Minute, "provider call timeout")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "dry-run")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "text")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "file")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "workflow")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "repo")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "issue")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "pr")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "optimize")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "optimize-profile")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "no-log")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "optimize-ollama")
	promptCmd.MarkFlagsMutuallyExclusive("validate-only", "history-file")
}

func validatePromptArgs(cmd *cobra.Command, args []string) error {
	autoRoute, _ := cmd.Flags().GetBool("auto")
	if autoRoute {
		if len(args) != 0 {
			return errors.New("prompt --auto does not accept an agent name")
		}
		return nil
	}
	return cobra.ExactArgs(1)(cmd, args)
}

func runPrompt(cmd *cobra.Command, args []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}

	dryRun, _ := cmd.Flags().GetBool("dry-run")
	validateOnly, _ := cmd.Flags().GetBool("validate-only")
	jsonOut, _ := cmd.Flags().GetBool("json")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	autoRoute, _ := cmd.Flags().GetBool("auto")
	if validateOnly && autoRoute {
		return errors.New("prompt --auto does not support --validate-only; select an agent explicitly")
	}

	if validateOnly {
		a, _, runtimeCfg, err := resolvePromptAgent(cmd, v, args, "")
		if err != nil {
			return err
		}
		a.Model = runtimeCfg.Model.Value
		a.APIKey = runtimeCfg.APIKey.Value
		a.BaseURL = runtimeCfg.BaseURL.Value
		target := agent.ResolveExecutionTarget(a)
		if err := validatePromptTarget(target, a, timeout); err != nil {
			return err
		}
		if jsonOut {
			_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
				"agent":    a.Name,
				"provider": a.Provider,
				"runner":   target.Runner,
				"backend":  effectivePromptBackend(a),
				"status":   "ok",
			})
			return nil
		}
		fmt.Printf("Backend validation OK (%s via %s)\n", effectivePromptBackend(a), target.Runner)
		return nil
	}

	text, workflowWarnings, err := resolvePromptInput(cmd)
	if err != nil {
		return err
	}
	for _, warningText := range workflowWarnings {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warningText)
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("prompt is empty")
	}

	a, decision, runtimeCfg, err := resolvePromptAgent(cmd, v, args, text)
	if err != nil {
		return err
	}
	a.Model = runtimeCfg.Model.Value
	a.APIKey = runtimeCfg.APIKey.Value
	a.BaseURL = runtimeCfg.BaseURL.Value
	target := agent.ResolveExecutionTarget(a)

	shared := v.SharedConfig()
	effectivePrompt := text
	optimizeEnabled, _ := cmd.Flags().GetBool("optimize")
	profileFlag, _ := cmd.Flags().GetString("optimize-profile")
	if shouldSkipOptimizationForWorkflow(cmd) {
		optimizeEnabled = false
	}
	optimizeOllamaCompat, _ := cmd.Flags().GetBool("optimize-ollama")
	if !optimizeOllamaCompat {
		optimizeEnabled = false
	}
	optimized := false
	optimizationProfile := ""
	if optimizeEnabled {
		effectivePrompt, optimizationProfile = optimizePromptForAgent(text, a, shared, profileFlag)
		optimized = true
	}

	if dryRun {
		if jsonOut {
			payload := map[string]any{
				"agent":            a.Name,
				"provider":         a.Provider,
				"runner":           target.Runner,
				"optimized":        optimized,
				"profile":          optimizationProfile,
				"effective_prompt": effectivePrompt,
				"value_sources": map[string]string{
					"model":    string(runtimeCfg.Model.Source),
					"api_key":  string(runtimeCfg.APIKey.Source),
					"base_url": string(runtimeCfg.BaseURL.Source),
				},
			}
			if decision != nil {
				payload["route"] = decision
			}
			_ = json.NewEncoder(os.Stdout).Encode(payload)
			return nil
		}
		if decision != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "routed to %q via %s (%s)\n", a.Name, target.Runner, decision.Intent.TaskClass)
		}
		fmt.Println(effectivePrompt)
		return nil
	}

	if decision != nil && !jsonOut {
		fmt.Fprintf(cmd.ErrOrStderr(), "routed to %q via %s (%s)\n", a.Name, target.Runner, decision.Intent.TaskClass)
	}
	result, execErr := executePromptTarget(target, a, effectivePrompt, timeout)

	record := PromptRecord{
		ID:                  fmt.Sprintf("prompt-%d", time.Now().UnixNano()),
		Timestamp:           time.Now().UTC(),
		AgentName:           a.Name,
		Provider:            string(a.Provider),
		Runner:              string(target.Runner),
		Model:               a.Model,
		Optimized:           optimized,
		OptimizationProfile: optimizationProfile,
		OriginalPrompt:      text,
		EffectivePrompt:     effectivePrompt,
		Success:             execErr == nil,
	}
	if decision != nil {
		record.RouterMode = decision.Mode
		record.RouteClass = decision.Intent.TaskClass
		record.RouteReasons = append([]string(nil), decision.Selected.Reasons...)
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
			payload := map[string]any{
				"record": record,
				"error":  execErr.Error(),
			}
			if decision != nil {
				payload["route"] = decision
			}
			_ = json.NewEncoder(os.Stdout).Encode(payload)
			return nil
		}
		return execErr
	}

	if jsonOut {
		payload := map[string]any{
			"record":   record,
			"response": result.Response,
		}
		if decision != nil {
			payload["route"] = decision
		}
		_ = json.NewEncoder(os.Stdout).Encode(payload)
		return nil
	}

	fmt.Println(result.Response)
	if usage := record.TokenUsage; usage != nil {
		fmt.Fprintf(os.Stderr, "tokens used: input=%d output=%d total=%d\n",
			usage.InputTokens, usage.OutputTokens, usage.TotalTokens)
	}
	return nil
}

func resolvePromptAgent(cmd *cobra.Command, v vaultLike, args []string, promptText string) (agent.Agent, *routerpkg.Decision, agent.PromptRuntimeConfig, error) {
	autoRoute, _ := cmd.Flags().GetBool("auto")
	if !autoRoute {
		a, ok := v.Get(args[0])
		if !ok {
			return agent.Agent{}, nil, agent.PromptRuntimeConfig{}, fmt.Errorf("agent %q not found", args[0])
		}
		return a, nil, agent.ResolvePromptRuntimeConfig(a), nil
	}

	if strings.TrimSpace(promptText) == "" {
		return agent.Agent{}, nil, agent.PromptRuntimeConfig{}, errors.New("prompt is empty")
	}
	routerCfg := promptRouterOverride(cmd)
	routingAgents := resolvedRoutingAgents(v.List())
	decision, err := routerpkg.Route(routerpkg.Request{
		Prompt: promptText,
		Agents: routingAgents,
		Shared: v.SharedConfig(),
		Config: routerCfg,
	})
	if err != nil {
		return agent.Agent{}, nil, agent.PromptRuntimeConfig{}, err
	}
	selected := decision.Selected.AgentConfig()
	original, ok := v.Get(selected.Name)
	if !ok {
		return agent.Agent{}, nil, agent.PromptRuntimeConfig{}, fmt.Errorf("selected agent %q not found", selected.Name)
	}
	runtimeCfg := agent.ResolvePromptRuntimeConfig(original)
	resolved := original
	if runtimeCfg.Model.Value != "" {
		resolved.Model = runtimeCfg.Model.Value
	}
	if runtimeCfg.APIKey.Value != "" {
		resolved.APIKey = runtimeCfg.APIKey.Value
	}
	if runtimeCfg.BaseURL.Value != "" {
		resolved.BaseURL = runtimeCfg.BaseURL.Value
	}
	target := agent.ResolveExecutionTarget(resolved)
	if routerCfg.LocalOnly && !target.Local {
		return agent.Agent{}, nil, agent.PromptRuntimeConfig{}, fmt.Errorf("selected agent %q does not satisfy local-only policy after runtime resolution", resolved.Name)
	}
	return original, &decision, runtimeCfg, nil
}

func promptRouterOverride(cmd *cobra.Command) agent.RouterConfig {
	mode, _ := cmd.Flags().GetString("router")
	langGraphCmd, _ := cmd.Flags().GetString("langgraph-cmd")
	preferLocal, _ := cmd.Flags().GetBool("prefer-local")
	preferFast, _ := cmd.Flags().GetBool("prefer-fast")
	preferLowCost, _ := cmd.Flags().GetBool("prefer-low-cost")
	localOnly, _ := cmd.Flags().GetBool("local-only")
	return agent.RouterConfig{
		Mode:          mode,
		LangGraphCmd:  langGraphCmd,
		PreferLocal:   preferLocal,
		PreferFast:    preferFast,
		PreferLowCost: preferLowCost,
		LocalOnly:     localOnly,
	}
}

type vaultLike interface {
	Get(name string) (agent.Agent, bool)
	List() []agent.Agent
	SharedConfig() agent.SharedConfig
}

func shouldSkipOptimizationForWorkflow(cmd *cobra.Command) bool {
	workflowFlag := cmd.Flags().Lookup("workflow")
	if workflowFlag == nil || !workflowFlag.Changed {
		return false
	}
	workflowName, _ := cmd.Flags().GetString("workflow")
	if strings.TrimSpace(workflowName) == "" {
		return false
	}
	optimizeFlag := cmd.Flags().Lookup("optimize")
	if optimizeFlag != nil && optimizeFlag.Changed {
		return false
	}
	profileFlag := cmd.Flags().Lookup("optimize-profile")
	return profileFlag == nil || !profileFlag.Changed
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
	text, provided, err := readOptionalPromptInput(cmd)
	if err != nil {
		return "", err
	}
	if !provided {
		return "", errors.New("no prompt provided; use --text, --file, or stdin")
	}
	return text, nil
}

func readOptionalPromptInput(cmd *cobra.Command) (string, bool, error) {
	text, _ := cmd.Flags().GetString("text")
	file, _ := cmd.Flags().GetString("file")

	if text != "" && file != "" {
		return "", false, errors.New("use either --text or --file, not both")
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", false, fmt.Errorf("reading prompt file: %w", err)
		}
		return string(data), true, nil
	}
	if text != "" {
		return text, true, nil
	}

	info, err := os.Stdin.Stat()
	if err == nil && (info.Mode()&os.ModeCharDevice) == 0 {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false, fmt.Errorf("reading stdin prompt: %w", err)
		}
		return string(data), true, nil
	}

	return "", false, nil
}

func executePrompt(a agent.Agent, prompt string, timeout time.Duration) (promptResult, error) {
	runtimeCfg := agent.ResolvePromptRuntimeConfig(a)
	if runtimeCfg.Model.Value != "" {
		a.Model = runtimeCfg.Model.Value
	}
	if runtimeCfg.APIKey.Value != "" {
		a.APIKey = runtimeCfg.APIKey.Value
	}
	if runtimeCfg.BaseURL.Value != "" {
		a.BaseURL = runtimeCfg.BaseURL.Value
	}
	target := agent.ResolveExecutionTarget(a)
	return executePromptTarget(target, a, prompt, timeout)
}

func executePromptTarget(target agent.ExecutionTarget, a agent.Agent, prompt string, timeout time.Duration) (promptResult, error) {
	switch target.Runner {
	case agent.RunnerOllamaHTTP:
		return executeOllamaPrompt(a, prompt, timeout)
	case agent.RunnerCodexCLI:
		return executeCodexPrompt(a, prompt, timeout)
	case agent.RunnerClaudeCLI:
		return executeClaudePrompt(a, prompt, timeout)
	case agent.RunnerOpenAIHTTP:
		return executeOpenAIPrompt(a, prompt, timeout)
	case agent.RunnerBedrockAPI:
		return promptResult{}, errors.New("bedrock backend execution is not supported yet")
	default:
		return promptResult{}, fmt.Errorf("runner %q (provider %q) is not supported by prompt gateway yet", target.Runner, a.Provider)
	}
}

func effectivePromptBackend(a agent.Agent) string {
	if a.Provider == agent.ProviderClaude {
		return agent.NormalizeClaudeBackend(a.Backend)
	}
	return string(a.Provider)
}

func validatePromptBackend(a agent.Agent, timeout time.Duration) error {
	return validatePromptTarget(agent.ResolveExecutionTarget(a), a, timeout)
}

func validatePromptTarget(target agent.ExecutionTarget, a agent.Agent, timeout time.Duration) error {
	switch target.Runner {
	case agent.RunnerOllamaHTTP:
		name := "ollama validation"
		if a.Provider == agent.ProviderClaude {
			name = "ollama backend validation"
		}
		return validateOllamaEndpoint(a.BaseURL, timeout, name)
	case agent.RunnerCodexCLI:
		if _, err := exec.LookPath("codex"); err != nil {
			return errors.New("codex binary not found in PATH")
		}
		return nil
	case agent.RunnerClaudeCLI:
		if _, err := exec.LookPath("claude"); err != nil {
			return errors.New("claude binary not found in PATH")
		}
		return nil
	case agent.RunnerOpenAIHTTP:
		return validateOpenAIEndpoint(a.BaseURL, a.APIKey, timeout)
	case agent.RunnerBedrockAPI:
		return errors.New("bedrock backend validation is not supported yet; validate AWS credentials manually")
	default:
		return fmt.Errorf("runner %q (provider %q) is not supported for validate-only", target.Runner, a.Provider)
	}
}

func validateOllamaEndpoint(baseURL string, timeout time.Duration, operationName string) error {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, baseURL+"/api/tags", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%s failed: %w", operationName, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s failed (%d): %s", operationName, resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
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

func executeOpenAIPrompt(a agent.Agent, prompt string, timeout time.Duration) (promptResult, error) {
	if strings.TrimSpace(a.Model) == "" {
		return promptResult{}, errors.New("openai agent requires model")
	}
	endpointURL, err := openAIEndpointURL(a.BaseURL, "chat/completions")
	if err != nil {
		return promptResult{}, err
	}
	payload := map[string]any{
		"model": a.Model,
		"messages": []map[string]string{{
			"role":    "user",
			"content": prompt,
		}},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return promptResult{}, fmt.Errorf("marshalling openai request payload: %w", err)
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodPost, endpointURL, bytes.NewReader(body))
	if err != nil {
		return promptResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(a.APIKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return promptResult{}, fmt.Errorf("calling openai: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return promptResult{}, fmt.Errorf("openai error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
			TotalTokens      int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return promptResult{}, fmt.Errorf("decoding openai response: %w", err)
	}
	response := ""
	if len(out.Choices) > 0 {
		response = strings.TrimSpace(out.Choices[0].Message.Content)
	}
	usage := agent.PromptTokenUsage{
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
		TotalTokens:  out.Usage.TotalTokens,
	}
	return promptResult{Response: response, Usage: usage}, nil
}

func validateOpenAIEndpoint(baseURL, apiKey string, timeout time.Duration) error {
	endpointURL, err := openAIEndpointURL(baseURL, "models")
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodGet, endpointURL, nil)
	if err != nil {
		return err
	}
	if key := strings.TrimSpace(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("openai validation failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("openai validation failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	return nil
}

func openAIEndpointURL(baseURL, endpoint string) (string, error) {
	normalized := strings.TrimSpace(baseURL)
	if normalized == "" {
		normalized = "https://api.openai.com"
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", fmt.Errorf("parsing openai base URL: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("openai base URL must include scheme and host")
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	if basePath == "" {
		basePath = "/v1"
	} else if basePath != "/v1" && !strings.HasSuffix(basePath, "/v1") {
		basePath = path.Join(basePath, "v1")
	}
	parsed.Path = path.Join(basePath, endpoint)
	parsed.RawPath = ""
	return parsed.String(), nil
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
	cmd.Env = envutil.SetValueWithPrecedence(os.Environ(), "OPENAI_API_KEY", strings.TrimSpace(a.APIKey))
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
	cmd.Env = envutil.SetValueWithPrecedence(os.Environ(), "ANTHROPIC_API_KEY", strings.TrimSpace(a.APIKey))

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
	return textutil.TruncateRunesWithEllipsis(trimmed, 500)
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
