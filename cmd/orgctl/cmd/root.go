// Package cmd implements the orgctl CLI commands.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/meridianhub/meridian/cmd/orgctl/client"
	"github.com/spf13/cobra"
)

// Sentinel errors for CLI operations.
var (
	// ErrConfirmRequired is returned when the --confirm flag is not provided.
	ErrConfirmRequired = errors.New("--confirm flag required")
	// ErrOrganizationNotFound is returned when an organization cannot be found.
	ErrOrganizationNotFound = errors.New("organization not found")
)

var (
	// serviceURL is the organization service URL (e.g., "localhost:50056").
	serviceURL string
	// timeout is the gRPC call timeout.
	timeout time.Duration
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "orgctl",
	Short: "Organization management CLI for Meridian platform",
	Long: `orgctl is a command-line tool for managing organizations in the Meridian platform.

It provides commands to register, retrieve, list, and manage the lifecycle of
organizations. Organizations represent tenants in the multi-tenant platform,
each with their own isolated PostgreSQL schema (org_{id}).

Examples:
  # Register a new organization
  orgctl register --id=acme_bank --name="Acme Bank" --settlement-asset=GBP

  # List all active organizations
  orgctl list --status=active

  # Get organization details
  orgctl get acme_bank

  # Deprovision an organization (mark as deprovisioned)
  orgctl deprovision acme_bank --confirm`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&serviceURL, "service-url", getEnvOrDefault("ORGANIZATION_SERVICE_URL", "localhost:50056"), "Organization service URL")
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 30*time.Second, "gRPC call timeout")
}

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// newClient creates a new OrganizationClient using the global flags.
func newClient() (*client.OrganizationClient, error) {
	cfg := client.Config{
		ServiceURL: serviceURL,
		Timeout:    timeout,
	}
	return client.NewOrganizationClient(context.Background(), cfg)
}

// handleGRPCError handles gRPC errors and prints user-friendly messages.
// Returns exit code: 0 for idempotent success, 1 for errors.
func handleGRPCError(err error, operation string) int {
	if err == nil {
		return 0
	}

	// Extract gRPC status code from error string for user-friendly messages
	errStr := err.Error()

	switch {
	case strings.Contains(errStr, "AlreadyExists"):
		fmt.Fprintf(os.Stderr, "Info: %s already exists (idempotent operation)\n", operation)
		return 0 // Idempotent success
	case strings.Contains(errStr, "NotFound"):
		fmt.Fprintf(os.Stderr, "Warning: %s not found (idempotent operation)\n", operation)
		return 0 // Idempotent success
	case strings.Contains(errStr, "InvalidArgument"):
		fmt.Fprintf(os.Stderr, "Error: Invalid input for %s: %v\n", operation, err)
		return 1
	case strings.Contains(errStr, "FailedPrecondition"):
		fmt.Fprintf(os.Stderr, "Error: Operation not allowed for %s: %v\n", operation, err)
		return 1
	case strings.Contains(errStr, "Unavailable"):
		fmt.Fprintf(os.Stderr, "Error: Organization service unavailable: %v\n", err)
		return 1
	case strings.Contains(errStr, "DeadlineExceeded"):
		fmt.Fprintf(os.Stderr, "Error: Request timeout for %s\n", operation)
		return 1
	default:
		fmt.Fprintf(os.Stderr, "Error: %s failed: %v\n", operation, err)
		return 1
	}
}
