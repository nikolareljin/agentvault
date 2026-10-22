package cmd

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/nikolareljin/agentvault/internal/agent"
	"github.com/spf13/cobra"
)

var configPricingCmd = &cobra.Command{
	Use:   "pricing",
	Short: "Manage the per-provider pricing used for cost estimates",
	Long: `Rates are USD per 1,000 tokens, per provider and an optional model pattern
(a substring of the model name; the longest matching pattern wins). While
no pricing is stored, built-in defaults are used. The first set stores the
defaults with the change, so the other providers keep their rates.

A monthly budget makes status --cost-report warn once this month's spend
for the provider reaches 80% of it.

Examples:
  agentvault config pricing list
  agentvault config pricing set claude --model sonnet --input 0.003 --output 0.015
  agentvault config pricing set ollama --input 0 --output 0 --monthly-budget 0
  agentvault config pricing remove claude --model sonnet`,
}

var configPricingListCmd = &cobra.Command{
	Use:   "list",
	Short: "List pricing rows (stored, or the defaults)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}
		rows := v.SharedConfig().Pricing
		if len(rows) == 0 {
			fmt.Println("No pricing stored; the built-in defaults apply:")
			rows = agent.DefaultPricing()
		}
		w := tabwriter.NewWriter(c1Writer(os.Stdout), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "PROVIDER\tMODEL\tINPUT/1K\tOUTPUT/1K\tCACHED/1K\tMONTHLY BUDGET")
		for _, p := range rows {
			model := p.ModelPattern
			if model == "" {
				model = "*"
			}
			budget := "-"
			if p.MonthlyBudgetUSD > 0 {
				budget = fmt.Sprintf("$%.2f", p.MonthlyBudgetUSD)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", printable(string(p.Provider)), printable(model),
				rate(p.InputPer1KTokens), rate(p.OutputPer1KTokens), rate(p.CachedPer1KTokens), budget)
		}
		return w.Flush()
	},
}

var configPricingSetCmd = &cobra.Command{
	Use:   "set <provider>",
	Short: "Add or change the pricing row for a provider and model pattern",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := agent.Provider(strings.TrimSpace(args[0]))
		if !slices.Contains(agent.ValidProviders(), provider) {
			return fmt.Errorf("unknown provider %q", args[0])
		}
		model, _ := cmd.Flags().GetString("model")
		// Matching ignores case, so Sonnet and sonnet are one row.
		row := agent.ProviderPricing{Provider: provider, ModelPattern: strings.ToLower(strings.TrimSpace(model))}
		for _, f := range []struct {
			name string
			dst  *float64
		}{
			{"input", &row.InputPer1KTokens},
			{"output", &row.OutputPer1KTokens},
			{"cached", &row.CachedPer1KTokens},
			{"monthly-budget", &row.MonthlyBudgetUSD},
		} {
			*f.dst, _ = cmd.Flags().GetFloat64(f.name)
			if *f.dst < 0 {
				return fmt.Errorf("--%s must not be negative", f.name)
			}
		}

		v, err := openVault()
		if err != nil {
			return err
		}
		shared := v.SharedConfig()
		seeded := len(shared.Pricing) == 0
		if seeded {
			shared.Pricing = agent.DefaultPricing()
		}
		key := pricingKey(row)
		i := slices.IndexFunc(shared.Pricing, func(p agent.ProviderPricing) bool { return pricingKey(p) == key })
		if i >= 0 {
			// A flag not given keeps the stored value.
			old := shared.Pricing[i]
			for name, pair := range map[string][2]*float64{
				"input":          {&row.InputPer1KTokens, &old.InputPer1KTokens},
				"output":         {&row.OutputPer1KTokens, &old.OutputPer1KTokens},
				"cached":         {&row.CachedPer1KTokens, &old.CachedPer1KTokens},
				"monthly-budget": {&row.MonthlyBudgetUSD, &old.MonthlyBudgetUSD},
			} {
				if !cmd.Flags().Changed(name) {
					*pair[0] = *pair[1]
				}
			}
			shared.Pricing[i] = row
		} else {
			if !cmd.Flags().Changed("input") || !cmd.Flags().Changed("output") {
				return fmt.Errorf("a new pricing row needs --input and --output")
			}
			shared.Pricing = append(shared.Pricing, row)
		}
		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}
		if seeded {
			fmt.Println("Stored the built-in default pricing, then applied this change.")
		}
		fmt.Printf("Pricing for %s updated.\n", pricingLabel(row))
		return nil
	},
}

var configPricingRemoveCmd = &cobra.Command{
	Use:   "remove <provider>",
	Short: "Remove the pricing row for a provider and model pattern",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		model, _ := cmd.Flags().GetString("model")
		row := agent.ProviderPricing{Provider: agent.Provider(strings.TrimSpace(args[0])), ModelPattern: strings.ToLower(strings.TrimSpace(model))}
		v, err := openVault()
		if err != nil {
			return err
		}
		shared := v.SharedConfig()
		if len(shared.Pricing) == 0 {
			return fmt.Errorf("no pricing stored; the built-in defaults apply and cannot be removed one by one")
		}
		key := pricingKey(row)
		i := slices.IndexFunc(shared.Pricing, func(p agent.ProviderPricing) bool { return pricingKey(p) == key })
		if i < 0 {
			return fmt.Errorf("no pricing row for %s", pricingLabel(row))
		}
		shared.Pricing = slices.Delete(shared.Pricing, i, i+1)
		if err := v.SetSharedConfig(shared); err != nil {
			return err
		}
		fmt.Printf("Pricing for %s removed.\n", pricingLabel(row))
		if len(shared.Pricing) == 0 {
			fmt.Println("No pricing rows left: the built-in defaults apply again.")
		}
		return nil
	},
}

// rate prints a rate as a plain decimal: 0.00003, not 3e-05.
func rate(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// pricingLabel names a pricing row as the commands take it.
func pricingLabel(p agent.ProviderPricing) string {
	if p.ModelPattern == "" {
		return printable(string(p.Provider))
	}
	return fmt.Sprintf("%s --model %s", printable(string(p.Provider)), printable(p.ModelPattern))
}

func init() {
	configCmd.AddCommand(configPricingCmd)
	configPricingCmd.AddCommand(configPricingListCmd, configPricingSetCmd, configPricingRemoveCmd)

	configPricingSetCmd.Flags().String("model", "", "model pattern: a substring of the model name (default: every model)")
	configPricingSetCmd.Flags().Float64("input", 0, "USD per 1,000 input tokens")
	configPricingSetCmd.Flags().Float64("output", 0, "USD per 1,000 output tokens")
	configPricingSetCmd.Flags().Float64("cached", 0, "USD per 1,000 cached input tokens")
	configPricingSetCmd.Flags().Float64("monthly-budget", 0, "monthly budget in USD for the whole provider, any model; 0 means none")
	configPricingRemoveCmd.Flags().String("model", "", "model pattern of the row to remove (default: the row for every model)")
}
