// Package cmd implements the tenantctl CLI commands.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/meridianhub/meridian/cmd/tenantctl/client"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/spf13/cobra"
)

// Sentinel errors for CLI operations.
var (
	// ErrConfirmRequired is returned when the --confirm flag is not provided.
	ErrConfirmRequired = errors.New("--confirm flag required")
	// ErrTenantNotFound is returned when a tenant cannot be found.
	ErrTenantNotFound = errors.New("tenant not found")
)

var (
	// serviceURL is the tenant service URL (e.g., "localhost:50056").
	serviceURL string
	// timeout is the gRPC call timeout.
	timeout time.Duration
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:   "tenantctl",
	Short: "Tenant management CLI for Meridian platform",
	Long: `tenantctl is a command-line tool for managing tenants in the Meridian platform.

It provides commands to register, retrieve, list, and manage the lifecycle of
tenants. Tenants represent platform tenants in the multi-tenant platform,
each with their own isolated PostgreSQL schema (org_{id}).

Examples:
  # Register a new tenant
  tenantctl register --id=acme_bank --name="Acme Bank" --settlement-asset=GBP

  # List all active tenants
  tenantctl list --status=active

  # Get tenant details
  tenantctl get acme_bank

  # Deprovision a tenant (mark as deprovisioned)
  tenantctl deprovision acme_bank --confirm`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&serviceURL, "service-url", getEnvOrDefault("TENANT_SERVICE_URL", fmt.Sprintf("localhost:%d", ports.Tenant)), "Tenant service URL")
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 30*time.Second, "gRPC call timeout")
}

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// newClient creates a new TenantClient using the global flags.
func newClient() (*client.TenantClient, error) {
	cfg := client.Config{
		ServiceURL: serviceURL,
		Timeout:    timeout,
	}
	return client.NewTenantClient(context.Background(), cfg)
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
		fmt.Fprintf(os.Stderr, "Error: Tenant service unavailable: %v\n", err)
		return 1
	case strings.Contains(errStr, "DeadlineExceeded"):
		fmt.Fprintf(os.Stderr, "Error: Request timeout for %s\n", operation)
		return 1
	default:
		fmt.Fprintf(os.Stderr, "Error: %s failed: %v\n", operation, err)
		return 1
	}
}
