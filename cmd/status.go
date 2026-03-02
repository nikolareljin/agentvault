package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	statuspkg "github.com/nikolareljin/agentvault/internal/status"
	"github.com/nikolareljin/agentvault/internal/vault"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show token usage and quota status for configured providers",
	Long: `Report token usage and available quotas per provider in a machine-readable format.

This command is designed for orchestration workflows where other tools need to
query AgentVault for provider capacity before scheduling agent work.

Examples:
  agentvault status --json
  AGENTVAULT_PASSWORD=... agentvault status --json
  agentvault status --no-vault --json`,
	RunE: runStatus,
}

func init() {
	rootCmd.AddCommand(statusCmd)
	statusCmd.Flags().Bool("json", true, "output as JSON")
	statusCmd.Flags().Bool("no-vault", false, "skip vault unlock and report provider status only")
	statusCmd.Flags().String("vault-password-env", "AGENTVAULT_PASSWORD", "environment variable containing vault password for non-interactive status calls")
}

func runStatus(cmd *cobra.Command, args []string) error {
	jsonOutput, _ := cmd.Flags().GetBool("json")
	noVault, _ := cmd.Flags().GetBool("no-vault")
	pwEnv, _ := cmd.Flags().GetString("vault-password-env")

	homeDir, err := os.UserHomeDir()
	if err != nil {
		homeDir = ""
	}

	var v *vault.Vault
	if !noVault {
		v, err = openVaultForStatus(pwEnv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: vault unavailable: %v\n", err)
		}
	}

	report := statuspkg.BuildReport(v, homeDir)
	if jsonOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	fmt.Printf("Generated: %s\n", report.GeneratedAt.Format("2006-01-02 15:04:05 MST"))
	if report.Vault != nil {
		fmt.Printf("Vault: %s\n", report.Vault.Path)
		fmt.Printf("  Agents: %d, Rules: %d, Roles: %d, Sessions: %d\n",
			report.Vault.Agents, report.Vault.Rules, report.Vault.Roles, report.Vault.Sessions)
	}

	fmt.Println("Providers:")
	providerNames := make([]string, 0, len(report.Providers))
	for name := range report.Providers {
		providerNames = append(providerNames, name)
	}
	sort.Strings(providerNames)
	for _, name := range providerNames {
		p := report.Providers[name]
		status := "unavailable"
		if p.Available {
			status = "ok"
		}
		fmt.Printf("  - %s: %s\n", name, status)
		if p.Tokens != nil {
			fmt.Printf("    tokens: total=%d input=%d cached=%d output=%d\n",
				p.Tokens.TotalTokens, p.Tokens.InputTokens, p.Tokens.CachedInputTokens, p.Tokens.OutputTokens)
		}
		if p.Quota != nil && p.Quota.Primary != nil {
			fmt.Printf("    quota(primary): used=%.1f%% remaining=%.1f%% reset=%s\n",
				p.Quota.Primary.UsedPercent,
				p.Quota.Primary.RemainingPercent,
				p.Quota.Primary.ResetsAtTime.Format("2006-01-02 15:04:05 MST"))
		}
		if p.Error != "" {
			fmt.Printf("    note: %s\n", p.Error)
		}
	}

	if len(report.Agents) > 0 {
		fmt.Println("Agents:")
		for _, a := range report.Agents {
			model := a.Model
			if strings.TrimSpace(model) == "" {
				model = "-"
			}
			fmt.Printf("  - %s (%s, model=%s): %s\n", a.Name, a.Provider, model, a.Status)
		}
	}

	return nil
}

func openVaultForStatus(passwordEnv string) (*vault.Vault, error) {
	vaultPath := resolveVaultPath()
	v := vault.New(vaultPath)
	if !v.Exists() {
		return nil, fmt.Errorf("vault not found at %s", vaultPath)
	}

	if passwordEnv != "" {
		if pw := strings.TrimSpace(os.Getenv(passwordEnv)); pw != "" {
			if err := v.Unlock(pw); err != nil {
				return nil, fmt.Errorf("unlock with %s failed: %w", passwordEnv, err)
			}
			return v, nil
		}
	}

	pw, err := readPassword("Master password: ")
	if err != nil {
		return nil, err
	}
	if err := v.Unlock(pw); err != nil {
		return nil, err
	}
	return v, nil
}
