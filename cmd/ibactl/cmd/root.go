// Package cmd implements the ibactl CLI commands for Internal Account management.
package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/meridianhub/meridian/services/internal-account/client"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/spf13/cobra"
)

var (
	// serviceURL is the internal account service URL.
	serviceURL string
	// timeout is the gRPC call timeout.
	timeout time.Duration
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "ibactl",
	Short: "Internal Account management CLI",
	Long: `ibactl is a command-line tool for managing internal accounts in Meridian.

It provides commands to provision default accounts, list accounts, and manage
internal account lifecycle. Internal accounts include clearing, nostro,
vostro, holding, suspense, revenue, expense, and inventory accounts.

Examples:
  # Provision default accounts for a tenant
  ibactl provision-defaults acme_bank

  # Provision default accounts for all active tenants
  ibactl provision-defaults --all

  # List internal accounts for a tenant
  ibactl list --tenant=acme_bank`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&serviceURL, "service-url",
		getEnvOrDefault("IBA_SERVICE_URL", fmt.Sprintf("localhost:%d", ports.InternalAccount)),
		"Internal Account service URL")
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 30*time.Second, "gRPC call timeout")
}

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// newClient creates a new InternalAccount client using global flags.
func newClient() (*client.Client, func(), error) {
	cfg := client.Config{
		Target:  serviceURL,
		Timeout: timeout,
	}
	return client.New(cfg)
}
