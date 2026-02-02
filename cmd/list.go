package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured agents",
	Long: `List all agents stored in the vault. Use --json for machine-readable output.
Use --show-keys to include API keys in the output (hidden by default).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		agents := v.List()
		if len(agents) == 0 {
			fmt.Println("No agents configured. Use 'agentvault add' to add one.")
			return nil
		}

		asJSON, _ := cmd.Flags().GetBool("json")
		showKeys, _ := cmd.Flags().GetBool("show-keys")

		if asJSON {
			type entry struct {
				Name     string `json:"name"`
				Provider string `json:"provider"`
				Model    string `json:"model"`
				APIKey   string `json:"api_key,omitempty"`
				BaseURL  string `json:"base_url,omitempty"`
			}
			var out []entry
			for _, a := range agents {
				e := entry{
					Name:     a.Name,
					Provider: string(a.Provider),
					Model:    a.Model,
					BaseURL:  a.BaseURL,
				}
				if showKeys {
					e.APIKey = a.APIKey
				}
				out = append(out, e)
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(out)
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tPROVIDER\tMODEL\tTAGS")
		for _, a := range agents {
			tags := "-"
			if len(a.Tags) > 0 {
				tags = fmt.Sprintf("%v", a.Tags)
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.Name, a.Provider, a.Model, tags)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
	listCmd.Flags().Bool("json", false, "output as JSON")
	listCmd.Flags().Bool("show-keys", false, "include API keys in output")
}
