package cmd

import "github.com/spf13/cobra"

// sagaCmd is the parent command for saga-related operations.
var sagaCmd = &cobra.Command{
	Use:   "saga",
	Short: "Starlark saga script tools",
	Long:  `Commands for working with Starlark saga scripts, including validation.`,
}

func init() {
	rootCmd.AddCommand(sagaCmd)
}
