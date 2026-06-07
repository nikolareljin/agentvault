package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
)

var capabilityCmd = &cobra.Command{
	Use:   "capability",
	Short: "Manage the model capability registry",
	Long: `Manage the model capability registry used by the llm-router to select agents.

The registry stores endpoint → model → capability mappings so the router knows
which local or remote endpoints host which models and what they can do.

Examples:
  agentvault capability list
  agentvault capability add --endpoint http://localhost:8080 --model llama3.2-1b --context 2048 --caps coding,general
  agentvault capability remove --endpoint http://localhost:8080 --model llama3.2-1b
  agentvault capability discover --endpoint http://localhost:8080`,
}

var capabilityListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all model capability entries",
	RunE:  runCapabilityList,
}

var capabilityAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a model capability entry",
	RunE:  runCapabilityAdd,
}

var capabilityRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a model capability entry",
	RunE:  runCapabilityRemove,
}

var capabilityDiscoverCmd = &cobra.Command{
	Use:   "discover",
	Short: "Auto-discover model capabilities from an endpoint (/v1/models or /health)",
	RunE:  runCapabilityDiscover,
}

func init() {
	rootCmd.AddCommand(capabilityCmd)
	capabilityCmd.AddCommand(capabilityListCmd)
	capabilityCmd.AddCommand(capabilityAddCmd)
	capabilityCmd.AddCommand(capabilityRemoveCmd)
	capabilityCmd.AddCommand(capabilityDiscoverCmd)

	capabilityListCmd.Flags().Bool("json", false, "output as JSON")

	capabilityAddCmd.Flags().String("endpoint", "", "endpoint base URL (required)")
	capabilityAddCmd.Flags().String("model", "", "model name (required)")
	capabilityAddCmd.Flags().Int("context", 0, "context window size in tokens")
	capabilityAddCmd.Flags().StringSlice("caps", nil, "capability tags: routing labels (coding,review,analysis,general) plus informational (vision,embedding)")
	_ = capabilityAddCmd.MarkFlagRequired("endpoint")
	_ = capabilityAddCmd.MarkFlagRequired("model")

	capabilityRemoveCmd.Flags().String("endpoint", "", "endpoint base URL (required)")
	capabilityRemoveCmd.Flags().String("model", "", "model name (required)")
	_ = capabilityRemoveCmd.MarkFlagRequired("endpoint")
	_ = capabilityRemoveCmd.MarkFlagRequired("model")

	capabilityDiscoverCmd.Flags().String("endpoint", "", "endpoint base URL to query (required)")
	capabilityDiscoverCmd.Flags().Duration("timeout", 10*time.Second, "request timeout")
	_ = capabilityDiscoverCmd.MarkFlagRequired("endpoint")
}

func runCapabilityList(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	entries := v.ListCapabilities()
	jsonOut, _ := cmd.Flags().GetBool("json")
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(entries)
	}
	if len(entries) == 0 {
		fmt.Println("No capability entries. Use `agentvault capability add` or `discover` to populate.")
		return nil
	}
	fmt.Printf("%-40s %-30s %8s %s\n", "Endpoint", "Model", "Context", "Capabilities")
	fmt.Println(strings.Repeat("-", 100))
	for _, e := range entries {
		ctx := "-"
		if e.ContextSize > 0 {
			ctx = fmt.Sprintf("%d", e.ContextSize)
		}
		caps := strings.Join(e.Capabilities, ",")
		if caps == "" {
			caps = "-"
		}
		fmt.Printf("%-40s %-30s %8s %s\n", e.EndpointURL, e.ModelName, ctx, caps)
	}
	return nil
}

func runCapabilityAdd(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	endpoint, _ := cmd.Flags().GetString("endpoint")
	model, _ := cmd.Flags().GetString("model")
	context, _ := cmd.Flags().GetInt("context")
	caps, _ := cmd.Flags().GetStringSlice("caps")

	normalized := make([]string, 0, len(caps))
	for _, c := range caps {
		c = strings.ToLower(strings.TrimSpace(c))
		if c != "" {
			normalized = append(normalized, c)
		}
	}

	entry := agent.ModelCapabilityEntry{
		EndpointURL:  strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		ModelName:    strings.TrimSpace(model),
		ContextSize:  context,
		Capabilities: normalized,
		Source:       "manual",
		UpdatedAt:    time.Now().UTC(),
	}
	if err := v.AddCapability(entry); err != nil {
		return err
	}
	fmt.Printf("Added: %s / %s\n", entry.EndpointURL, entry.ModelName)
	return nil
}

func runCapabilityRemove(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	endpoint, _ := cmd.Flags().GetString("endpoint")
	model, _ := cmd.Flags().GetString("model")
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	model = strings.TrimSpace(model)
	if err := v.RemoveCapability(endpoint, model); err != nil {
		return err
	}
	fmt.Printf("Removed: %s / %s\n", endpoint, model)
	return nil
}

// runCapabilityDiscover queries the endpoint's /health (llm-gateway-helpers shape) or
// /v1/models (OpenAI-compat shape) and creates capability entries for each reported model.
func runCapabilityDiscover(cmd *cobra.Command, _ []string) error {
	v, err := openVault()
	if err != nil {
		return err
	}
	endpoint, _ := cmd.Flags().GetString("endpoint")
	timeout, _ := cmd.Flags().GetDuration("timeout")
	baseURL := strings.TrimRight(strings.TrimSpace(endpoint), "/")

	client := &http.Client{Timeout: timeout}

	// Try OpenAI-compat /v1/models first (llama.cpp, bitnet.cpp, Ollama).
	entries, modelsErr := discoverFromModelsEndpoint(client, baseURL)
	if modelsErr != nil {
		// Fall back to /health (llm-gateway-helpers).
		var healthErr error
		entries, healthErr = discoverFromHealthEndpoint(client, baseURL)
		if healthErr != nil {
			return fmt.Errorf("discover: could not query %s — /v1/models: %v; /health: %v", baseURL, modelsErr, healthErr)
		}
	}

	if len(entries) == 0 {
		fmt.Println("No models discovered.")
		return nil
	}

	total := len(entries)
	added := 0
	for _, entry := range entries {
		entry.EndpointURL = baseURL
		entry.Source = "auto-discovered"
		entry.UpdatedAt = time.Now().UTC()
		if err := v.AddCapability(entry); err != nil {
			fmt.Fprintf(os.Stderr, "  skip %s: %v\n", entry.ModelName, err)
			continue
		}
		fmt.Printf("  + %s (%s)\n", entry.ModelName, strings.Join(entry.Capabilities, ","))
		added++
	}
	skipped := total - added
	if skipped > 0 {
		fmt.Printf("Added %d of %d model(s) from %s (%d already in registry)\n", added, total, baseURL, skipped)
	} else {
		fmt.Printf("Added %d model(s) from %s\n", added, baseURL)
	}
	return nil
}

func discoverFromModelsEndpoint(client *http.Client, baseURL string) ([]agent.ModelCapabilityEntry, error) {
	resp, err := client.Get(baseURL + "/v1/models")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if len(out.Data) == 0 {
		return nil, errors.New("no models in response")
	}
	entries := make([]agent.ModelCapabilityEntry, 0, len(out.Data))
	for _, m := range out.Data {
		if strings.TrimSpace(m.ID) == "" {
			continue
		}
		entries = append(entries, agent.ModelCapabilityEntry{
			ModelName:    m.ID,
			Capabilities: inferCapabilities(m.ID),
		})
	}
	return entries, nil
}

func discoverFromHealthEndpoint(client *http.Client, baseURL string) ([]agent.ModelCapabilityEntry, error) {
	resp, err := client.Get(baseURL + "/health")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	// llm-gateway-helpers /health shape: {"status":"ok","models":["llama3.2",...]}
	var out struct {
		Models []string `json:"models"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, err
	}
	if len(out.Models) == 0 {
		return nil, errors.New("no models in /health response")
	}
	entries := make([]agent.ModelCapabilityEntry, 0, len(out.Models))
	for _, m := range out.Models {
		if strings.TrimSpace(m) == "" {
			continue
		}
		entries = append(entries, agent.ModelCapabilityEntry{
			ModelName:    m,
			Capabilities: inferCapabilities(m),
		})
	}
	return entries, nil
}

// inferCapabilities guesses capability tags from a model name.
// Tags use the routing vocabulary (agent.RouteCapability* constants) so they
// directly influence routing scores when merged into a profile's Capabilities.
func inferCapabilities(model string) []string {
	m := strings.ToLower(model)
	caps := []string{"general"}
	if strings.Contains(m, "code") || strings.Contains(m, "codex") || strings.Contains(m, "coder") || strings.Contains(m, "starcoder") || strings.Contains(m, "deepseek-coder") {
		caps = append(caps, "coding")
	}
	if strings.Contains(m, "vision") || strings.Contains(m, "vl") || strings.Contains(m, "llava") || strings.Contains(m, "visual") {
		caps = append(caps, "vision")
	}
	if strings.Contains(m, "embed") {
		caps = append(caps, "embedding")
	}
	if strings.Contains(m, "reasoning") || strings.Contains(m, "think") || strings.Contains(m, "r1") {
		caps = append(caps, "analysis")
	}
	return caps
}
