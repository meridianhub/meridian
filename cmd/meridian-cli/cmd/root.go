// Package cmd implements the meridian-cli CLI commands.
package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version information set at build time.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:     "meridian-cli",
	Short:   "Meridian platform CLI tools",
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", Version, GitCommit, BuildDate),
	Long: `meridian-cli provides command-line tools for the Meridian platform.

Available command groups:
  saga    Starlark saga script tools (validate, etc.)

Exit Codes:
  0 - Success
  1 - Failure (validation failed or error occurred)`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
