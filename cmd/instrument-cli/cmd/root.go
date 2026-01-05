// Package cmd implements the instrument-cli CLI commands.
package cmd

import (
	"os"

	"github.com/spf13/cobra"
)

// serviceURL is the reference data service URL.
var serviceURL string

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "instrument-cli",
	Short: "Instrument simulation CLI for Meridian platform",
	Long: `instrument-cli is a command-line tool for simulating instrument transactions
in the Meridian platform.

It provides dry-run capabilities to test validation rules, bucket key generation,
and position previews without persisting data. This is useful for:

  - Testing CEL expressions before activating instruments
  - Debugging validation failures in production
  - Understanding how attributes map to bucket IDs
  - Previewing position records before creation

Examples:
  # Simulate a USD transaction
  instrument-cli simulate --tenant=acme_bank --instrument=USD --amount=100.00

  # Simulate with attributes for bucket key generation
  instrument-cli simulate --tenant=acme_bank --instrument=CARBON_CREDIT \
    --amount=50.00 --attr=vintage_year=2024 --attr=registry=VERRA

  # Simulate with validity period
  instrument-cli simulate --tenant=acme_bank --instrument=VOUCHER \
    --amount=10 --valid-from=2024-01-01T00:00:00Z --valid-to=2024-12-31T23:59:59Z`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&serviceURL, "service-url", getEnvOrDefault("REFERENCE_DATA_SERVICE_URL", "localhost:8080"), "Reference data service URL")
}

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}
