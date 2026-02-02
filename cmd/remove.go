package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var removeCmd = &cobra.Command{
	Use:   "remove [name]",
	Short: "Remove an agent from the vault",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		v, err := openVault()
		if err != nil {
			return err
		}

		name := args[0]
		if _, ok := v.Get(name); !ok {
			return fmt.Errorf("agent %q not found", name)
		}

		force, _ := cmd.Flags().GetBool("force")
		if !force {
			fmt.Printf("Remove agent %q? This cannot be undone. Use --force to skip this prompt.\n", name)
			fmt.Print("Type the agent name to confirm: ")
			var confirm string
			if _, err := fmt.Scanln(&confirm); err != nil {
				return fmt.Errorf("reading confirmation: %w", err)
			}
			if confirm != name {
				return fmt.Errorf("confirmation did not match; aborting")
			}
		}

		if err := v.Remove(name); err != nil {
			return err
		}
		fmt.Printf("Agent %q removed.\n", name)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(removeCmd)
	removeCmd.Flags().BoolP("force", "f", false, "skip confirmation prompt")
}
