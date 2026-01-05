// Package cmd implements the instrument-cli CLI commands.
package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/spf13/cobra"
)

// Version information set at build time.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// Global flags.
var (
	serviceURL   string
	timeout      time.Duration
	insecureMode bool
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:     "instrument-cli",
	Short:   "Instrument simulation CLI for Meridian platform",
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", Version, GitCommit, BuildDate),
	Long: `instrument-cli is a command-line tool for simulating instrument transactions
in the Meridian platform.

It provides dry-run capabilities to test validation rules, bucket key generation,
and position previews without persisting data. This is useful for:

  - Testing CEL expressions before activating instruments
  - Debugging validation failures in production
  - Understanding how attributes map to bucket IDs
  - Previewing position records before creation

Exit Codes:
  0 - Success (validation passed)
  1 - Failure (validation failed or error occurred)

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
	rootCmd.PersistentFlags().StringVar(&serviceURL, "service-url",
		getEnvOrDefault("REFERENCE_DATA_SERVICE_URL", fmt.Sprintf("localhost:%d", ports.Gateway)),
		"Reference data service URL")
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 30*time.Second,
		"gRPC call timeout")
	rootCmd.PersistentFlags().BoolVar(&insecureMode, "insecure", false,
		"Use insecure connection (no TLS). Required for local development.")
}

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// handleGRPCError handles gRPC errors and prints user-friendly messages.
// Returns exit code: 0 for success, 1 for errors.
func handleGRPCError(err error, operation string) int {
	if err == nil {
		return 0
	}

	errStr := err.Error()

	switch {
	case strings.Contains(errStr, "NotFound"):
		fmt.Fprintf(os.Stderr, "Error: %s not found\n", operation)
		return 1
	case strings.Contains(errStr, "InvalidArgument"):
		fmt.Fprintf(os.Stderr, "Error: Invalid input for %s: %v\n", operation, err)
		return 1
	case strings.Contains(errStr, "FailedPrecondition"):
		fmt.Fprintf(os.Stderr, "Error: Operation not allowed for %s: %v\n", operation, err)
		return 1
	case strings.Contains(errStr, "Unavailable"):
		fmt.Fprintf(os.Stderr, "Error: Reference Data service unavailable: %v\n", err)
		return 1
	case strings.Contains(errStr, "DeadlineExceeded"):
		fmt.Fprintf(os.Stderr, "Error: Request timeout for %s\n", operation)
		return 1
	case strings.Contains(errStr, "connection refused"):
		fmt.Fprintf(os.Stderr, "Error: Cannot connect to %s. Is the service running?\n", serviceURL)
		return 1
	default:
		fmt.Fprintf(os.Stderr, "Error: %s failed: %v\n", operation, err)
		return 1
	}
}
