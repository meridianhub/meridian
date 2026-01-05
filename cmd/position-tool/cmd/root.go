// Package cmd implements the position-tool CLI commands.
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
	// ErrDBURLRequired is returned when --db-url flag is missing and DATABASE_URL is not set.
	ErrDBURLRequired = errors.New("--db-url is required (or set DATABASE_URL environment variable)")
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
	tenantID string
	dryRun   bool
	logLevel string
	dbURL    string
)

// rootCmd represents the base command when called without any subcommands.
var rootCmd = &cobra.Command{
	Use:     "position-tool",
	Short:   "Bulk position management CLI for Meridian platform",
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", Version, GitCommit, BuildDate),
	Long: `position-tool is a command-line tool for bulk position management
in the Meridian platform.

It provides commands for:

  - import:   Bulk import positions from CSV files with validation
  - export:   Export positions to CSV with filtering options
  - rebucket: Recalculate bucket keys for existing positions

Features:

  - Progress reporting with estimated time remaining
  - Graceful shutdown with checkpoint support
  - Dry-run mode for validation without persistence
  - Comprehensive validation error reports
  - Resume capability for interrupted imports

Exit Codes:
  0 - Success
  1 - Failure (validation failed or error occurred)

Examples:
  # Import positions from CSV
  position-tool import --tenant=acme_bank --source=positions.csv

  # Dry-run import to validate without persisting
  position-tool import --tenant=acme_bank --source=positions.csv --dry-run

  # Export positions for a specific instrument
  position-tool export --tenant=acme_bank --instrument=USD --output=export.csv

  # Rebucket positions after instrument definition change
  position-tool rebucket --tenant=acme_bank --instrument=CARBON_CREDIT`,
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
	rootCmd.PersistentFlags().StringVar(&dbURL, "db-url",
		getEnvOrDefault("DATABASE_URL", ""),
		"Database connection URL (required unless set via DATABASE_URL env)")
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
		sig := <-sigChan
		slog.Info("received shutdown signal", "signal", sig)
		cancel()
		signal.Stop(sigChan)
	}()

	return ctx, cancel
}

// validateCommonFlags validates that required common flags are set.
func validateCommonFlags() error {
	if tenantID == "" {
		return ErrTenantRequired
	}
	if dbURL == "" {
		return ErrDBURLRequired
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
