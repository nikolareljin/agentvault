package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LocalAIAnalysis is the structured classification returned by a local AI routing call.
type LocalAIAnalysis struct {
	Complexity       int    `json:"complexity"` // 1–10, higher = more complex
	TaskType         string `json:"task_type"`  // coding|review|analysis|general|question
	Urgency          string `json:"urgency"`    // low|medium|high
	EstimatedTokens  int    `json:"estimated_tokens"`
	PrivacySensitive bool   `json:"privacy_sensitive"`
	NeedsTools       bool   `json:"needs_tools"`
}

const localAIClassifySystemPrompt = `You are a routing classifier. Analyze prompts and return ONLY valid JSON with no extra text, explanation, or markdown fences.

Required JSON fields:
- complexity: integer 1-10 (1=trivial question, 10=large multi-file refactor)
- task_type: one of coding, review, analysis, general, question
- urgency: one of low, medium, high
- estimated_tokens: integer estimate of output tokens needed
- privacy_sensitive: boolean (true if prompt contains personal data or secrets)
- needs_tools: boolean (true if task requires file edits, running commands, or web search)`

// AnalyzeWithLocalAI calls a local Ollama instance to classify a prompt for routing decisions.
// Returns a zero LocalAIAnalysis and an error if Ollama is unreachable or returns unparseable output.
func AnalyzeWithLocalAI(prompt, ollamaBaseURL, model string, timeout time.Duration) (LocalAIAnalysis, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(ollamaBaseURL), "/")
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if strings.TrimSpace(model) == "" {
		model = "llama3.2"
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	classifyInput := localAIClassifySystemPrompt + "\n\nPrompt to classify:\n" + strings.TrimSpace(prompt)
	payload := map[string]any{
		"model":  model,
		"prompt": classifyInput,
		"stream": false,
		"format": "json",
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return LocalAIAnalysis{}, fmt.Errorf("local-ai: marshal request: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return LocalAIAnalysis{}, fmt.Errorf("local-ai: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return LocalAIAnalysis{}, fmt.Errorf("local-ai: call ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return LocalAIAnalysis{}, fmt.Errorf("local-ai: ollama error %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var ollamaOut struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ollamaOut); err != nil {
		return LocalAIAnalysis{}, fmt.Errorf("local-ai: decode ollama response: %w", err)
	}

	rawResponse := strings.TrimSpace(ollamaOut.Response)
	// Strip markdown code fences if the model included them despite instructions
	rawResponse = stripJSONFences(rawResponse)

	var analysis LocalAIAnalysis
	if err := json.Unmarshal([]byte(rawResponse), &analysis); err != nil {
		return LocalAIAnalysis{}, fmt.Errorf("local-ai: parse analysis JSON %q: %w", rawResponse, err)
	}

	return normalizeLocalAIAnalysis(analysis), nil
}

func stripJSONFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if idx := strings.Index(s[3:], "\n"); idx >= 0 {
			s = s[3+idx+1:]
		}
		s = strings.TrimSuffix(strings.TrimSpace(s), "```")
	}
	return strings.TrimSpace(s)
}

func normalizeLocalAIAnalysis(a LocalAIAnalysis) LocalAIAnalysis {
	if a.Complexity < 1 {
		a.Complexity = 1
	}
	if a.Complexity > 10 {
		a.Complexity = 10
	}
	switch strings.ToLower(strings.TrimSpace(a.TaskType)) {
	case "coding", "review", "analysis", "general", "question":
		a.TaskType = strings.ToLower(strings.TrimSpace(a.TaskType))
	default:
		a.TaskType = "general"
	}
	switch strings.ToLower(strings.TrimSpace(a.Urgency)) {
	case "low", "medium", "high":
		a.Urgency = strings.ToLower(strings.TrimSpace(a.Urgency))
	default:
		a.Urgency = "medium"
	}
	return a
}

// enrichIntentFromLocalAI updates an Intent with the results of a local AI classification.
func enrichIntentFromLocalAI(intent *Intent, analysis LocalAIAnalysis) {
	if analysis.PrivacySensitive {
		intent.PrivacySensitive = true
	}
	if analysis.Urgency == "high" {
		intent.LatencySensitive = true
	}
	switch analysis.TaskType {
	case "coding":
		intent.Coding = true
		intent.TaskClass = "coding"
	case "review":
		intent.Review = true
		intent.TaskClass = "review"
	case "analysis":
		intent.Analysis = true
		intent.TaskClass = "analysis"
	}
	// Very complex tasks always benefit from analysis capability even if they are coding tasks
	if analysis.Complexity >= 8 && !intent.Analysis {
		intent.Analysis = true
	}
}
