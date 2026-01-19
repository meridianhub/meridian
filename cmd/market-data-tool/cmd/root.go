// Package cmd implements the market-data-tool CLI commands.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// Sentinel errors for CLI validation.
var (
	// ErrTenantRequired is returned when --tenant flag is missing.
	ErrTenantRequired = errors.New("--tenant is required")
	// ErrInvalidLogLevel is returned when an invalid log level is specified.
	ErrInvalidLogLevel = errors.New("invalid log level")
)

// Version information set at build time.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// Global flags shared across all subcommands.
var (
	tenantID     string
	dryRun       bool
	logLevel     string
	grpcEndpoint string
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:     "market-data-tool",
	Short:   "Bulk market data import CLI for Meridian platform",
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", Version, GitCommit, BuildDate),
	Long: `market-data-tool is a command-line tool for bulk market data import
in the Meridian platform.

It provides commands for:

  - import:   Bulk import observations from CSV files via gRPC
  - validate: Dry-run validation preview (shows what service will validate)
  - schema:   Query expected CSV format from DataSetService

Features:

  - Progress reporting with estimated time remaining
  - Graceful shutdown with checkpoint support
  - Dry-run mode for validation preview
  - Comprehensive validation error reports
  - Resume capability for interrupted imports
  - Dynamic attribute schema validation

Exit Codes:
  0 - Success
  1 - Failure (validation failed or error occurred)

Examples:
  # Import observations from CSV
  market-data-tool import --tenant=acme_corp --source=rates.csv --dataset=USD_EUR_FX --source-code=BLOOMBERG

  # Dry-run import to validate without persisting
  market-data-tool import --tenant=acme_corp --source=rates.csv --dataset=USD_EUR_FX --dry-run

  # Query expected CSV format for a dataset
  market-data-tool schema --tenant=acme_corp --dataset=USD_EUR_FX

  # Validate CSV against dataset schema
  market-data-tool validate --tenant=acme_corp --source=rates.csv --dataset=USD_EUR_FX`,
	PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
		// Configure logging based on --log-level flag
		return configureLogging(logLevel)
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	// Common flags available to all subcommands
	rootCmd.PersistentFlags().StringVar(&tenantID, "tenant", "",
		"Tenant ID (required)")
	rootCmd.PersistentFlags().BoolVar(&dryRun, "dry-run", false,
		"Validate and report without persisting changes")
	rootCmd.PersistentFlags().StringVar(&logLevel, "log-level", "info",
		"Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().StringVar(&grpcEndpoint, "grpc-endpoint",
		getEnvOrDefault("MARKET_INFORMATION_URL", "localhost:9090"),
		"gRPC endpoint for Market Information Service")
}

// getEnvOrDefault returns the environment variable value or a default.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// configureLogging sets up the slog default logger with the specified level.
func configureLogging(level string) error {
	var logLvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		logLvl = slog.LevelDebug
	case "info":
		logLvl = slog.LevelInfo
	case "warn", "warning":
		logLvl = slog.LevelWarn
	case "error":
		logLvl = slog.LevelError
	default:
		return fmt.Errorf("%w: %s (valid: debug, info, warn, error)", ErrInvalidLogLevel, level)
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: logLvl,
	})
	slog.SetDefault(slog.New(handler))
	return nil
}

// ShutdownContext returns a context that is cancelled when a shutdown signal
// (SIGINT, SIGTERM) is received. The returned cancel function should be deferred
// to clean up resources.
//
// Commands should use this to enable graceful shutdown and checkpoint progress:
//
//	ctx, cancel := cmd.ShutdownContext()
//	defer cancel()
//	// Use ctx for operations that should be interruptible
func ShutdownContext() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		select {
		case sig := <-sigChan:
			slog.Info("received shutdown signal", "signal", sig)
			cancel()
		case <-ctx.Done():
			// Context was cancelled by caller, clean up signal handling
		}
		signal.Stop(sigChan)
	}()

	return ctx, cancel
}

// validateCommonFlags validates that required common flags are set.
func validateCommonFlags() error {
	if tenantID == "" {
		return ErrTenantRequired
	}
	return nil
}

// formatDuration formats a duration for human-readable display.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	if d < time.Hour {
		mins := int(d.Minutes())
		secs := int(d.Seconds()) % 60
		return fmt.Sprintf("%dm%ds", mins, secs)
	}
	hours := int(d.Hours())
	mins := int(d.Minutes()) % 60
	return fmt.Sprintf("%dh%dm", hours, mins)
}
