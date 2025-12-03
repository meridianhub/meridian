// Package main implements the Horizon Integrity Proof CLI tool.
// This tool demonstrates Meridian's resilience against phantom transactions
// by simulating network failures and verifying idempotency guarantees.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// Build information set via ldflags during compilation.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Configuration validation errors.
var (
	ErrTargetEmpty        = errors.New("target address cannot be empty")
	ErrTimeoutNotPositive = errors.New("timeout must be positive")
	ErrTimeoutTooShort    = errors.New("timeout too short (minimum 5ms)")
	ErrTimeoutTooLong     = errors.New("timeout too long for sabotage simulation (maximum 10s)")
	ErrAmountNotPositive  = errors.New("amount must be positive")
	ErrAmountTooLarge     = errors.New("amount exceeds maximum (1000000 GBP)")
	ErrOutputEmpty        = errors.New("output path cannot be empty")
)

// Config holds the CLI configuration parsed from flags.
type Config struct {
	// Target is the gRPC target address for services
	Target string
	// Timeout is the client-side timeout for the sabotage attempt
	Timeout time.Duration
	// Amount is the payment amount in pence
	Amount int64
	// Output is the path for the JSON report
	Output string
	// Verbose enables debug logging
	Verbose bool
	// NoCleanup skips test account cleanup after demo
	NoCleanup bool
}

// DefaultConfig returns the default configuration.
func DefaultConfig() *Config {
	return &Config{
		Target:    "localhost:50051",
		Timeout:   30 * time.Millisecond,
		Amount:    10000, // GBP 100.00 in pence
		Output:    "./integrity_report.json",
		Verbose:   false,
		NoCleanup: false,
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := DefaultConfig()

	rootCmd := &cobra.Command{
		Use:   "horizon-demo",
		Short: "Horizon Integrity Proof - Resilience Demonstration",
		Long: `Horizon Integrity Proof demonstrates Meridian's resilience against
phantom transactions (the "Post Office Horizon" problem).

This tool simulates a network failure during payment processing and verifies
that idempotency guarantees prevent double-spending. It proves that even when
a client loses connection, retrying with the same idempotency key returns the
original result without creating duplicate transactions.

The demo:
1. Creates a test account with GBP 1,000.00
2. Initiates a GBP 100.00 payment with an aggressive timeout (simulated network cut)
3. Retries the same payment with the same idempotency key
4. Verifies: balance is GBP 900.00 (not GBP 800.00 from double-spend)
5. Generates a forensic audit report`,
		Version: fmt.Sprintf("%s (commit: %s, built: %s)", Version, Commit, BuildDate),
		RunE: func(_ *cobra.Command, _ []string) error {
			return runDemo(cfg)
		},
	}

	// Define flags
	flags := rootCmd.Flags()
	flags.StringVar(&cfg.Target, "target", cfg.Target,
		"gRPC target address for CurrentAccount and PaymentOrder services")
	flags.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout,
		"client-side timeout for sabotage attempt (simulated network failure)")
	flags.Int64Var(&cfg.Amount, "amount", cfg.Amount,
		"payment amount in pence (default 10000 = GBP 100.00)")
	flags.StringVarP(&cfg.Output, "output", "o", cfg.Output,
		"path for JSON integrity report")
	flags.BoolVarP(&cfg.Verbose, "verbose", "v", cfg.Verbose,
		"enable verbose/debug logging")
	flags.BoolVar(&cfg.NoCleanup, "no-cleanup", cfg.NoCleanup,
		"skip test account cleanup after demo (useful for debugging)")

	return rootCmd.Execute()
}

// runDemo executes the Horizon integrity proof demonstration.
func runDemo(cfg *Config) error {
	// Initialize logger based on verbosity
	logLevel := slog.LevelInfo
	if cfg.Verbose {
		logLevel = slog.LevelDebug
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting Horizon Integrity Proof",
		"version", Version,
		"target", cfg.Target,
		"timeout", cfg.Timeout,
		"amount_pence", cfg.Amount,
		"output", cfg.Output,
		"verbose", cfg.Verbose,
		"no_cleanup", cfg.NoCleanup,
	)

	// Validate configuration
	if err := validateConfig(cfg); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// TODO: Implement demo logic in subsequent tasks
	// Task 2: gRPC client setup
	// Task 3: Pre-flight setup (create account, deposit)
	// Task 4: Sabotage attempt with timeout
	// Task 5: Idempotency retry
	// Task 6-7: Forensic audit
	// Task 9-10: Report generation

	logger.Info("demo completed successfully")
	return nil
}

// validateConfig validates the CLI configuration.
func validateConfig(cfg *Config) error {
	if cfg.Target == "" {
		return ErrTargetEmpty
	}

	if cfg.Timeout <= 0 {
		return fmt.Errorf("%w: got %v", ErrTimeoutNotPositive, cfg.Timeout)
	}

	if cfg.Timeout < 5*time.Millisecond {
		return fmt.Errorf("%w: got %v", ErrTimeoutTooShort, cfg.Timeout)
	}

	if cfg.Timeout > 10*time.Second {
		return fmt.Errorf("%w: got %v", ErrTimeoutTooLong, cfg.Timeout)
	}

	if cfg.Amount <= 0 {
		return fmt.Errorf("%w: got %d", ErrAmountNotPositive, cfg.Amount)
	}

	if cfg.Amount > 100000000 { // GBP 1,000,000.00 max
		return fmt.Errorf("%w: got %d pence", ErrAmountTooLarge, cfg.Amount)
	}

	if cfg.Output == "" {
		return ErrOutputEmpty
	}

	return nil
}
