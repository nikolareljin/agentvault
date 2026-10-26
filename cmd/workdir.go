package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/nikolareljin/agentvault/internal/locations"
	"github.com/spf13/cobra"
)

var workdirCmd = &cobra.Command{
	Use:   "workdir",
	Short: "Declare named work directories (personal, one per employer or client)",
	Long: `A work directory is a folder of projects, such as ~/Projects. Declaring
one by name keeps its settings, memory and plugins apart from the others: a
personal directory and an employer's never share them.

The path is this machine's. The vault keeps the declarations; a bundle does
not carry them.

Examples:
  agentvault workdir add personal ~/Projects
  agentvault workdir add company-a ~/work/company-a
  agentvault workdir list
  agentvault workdir remove company-a`,
}

var workdirAddCmd = &cobra.Command{
	Use:   "add <name> <path>",
	Short: "Declare a work directory by name",
	Long: `Declare PATH as the work directory NAME. NAME is lowercase letters, digits
and '-', at most 32. PATH must be an existing directory, and may not be the
same as, inside, or around another declared work directory.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		v, err := openVault()
		if err != nil {
			return err
		}
		set, err := v.Locations().Declare(args[0], args[1])
		if err != nil {
			// A path from the vault or the command line may hold control
			// characters; the error reaches the terminal.
			return errors.New(printable(err.Error()))
		}
		if err := v.SetLocations(set); err != nil {
			return err
		}
		n, _ := set.Lookup(args[0])
		fmt.Fprintf(c1Writer(os.Stdout), "Work dir %s declared at %s.\n", printable(n.Name), printable(n.Path))
		return nil
	},
}

var workdirListCmd = &cobra.Command{
	Use:   "list",
	Short: "List declared work directories",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		v, err := openVault()
		if err != nil {
			return err
		}
		named := v.Locations().Named
		if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
			if named == nil {
				named = []locations.Named{}
			}
			enc := json.NewEncoder(c1Writer(os.Stdout))
			enc.SetIndent("", "  ")
			return enc.Encode(named)
		}
		if len(named) == 0 {
			fmt.Println("No work dirs declared.")
			return nil
		}
		w := tabwriter.NewWriter(c1Writer(os.Stdout), 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tPATH")
		for _, n := range named {
			fmt.Fprintf(w, "%s\t%s\n", printable(n.Name), printable(n.Path))
		}
		return w.Flush()
	},
}

var workdirRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Forget a declared work directory (its files are not touched)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		v, err := openVault()
		if err != nil {
			return err
		}
		set, ok := v.Locations().Undeclare(args[0])
		if !ok {
			return fmt.Errorf("no work dir named %s", printable(args[0]))
		}
		if err := v.SetLocations(set); err != nil {
			return err
		}
		fmt.Printf("Work dir %s removed.\n", printable(args[0]))
		return nil
	},
}

func init() {
	rootCmd.AddCommand(workdirCmd)
	workdirCmd.AddCommand(workdirAddCmd, workdirListCmd, workdirRemoveCmd)
	workdirListCmd.Flags().Bool("json", false, "print JSON")
}
