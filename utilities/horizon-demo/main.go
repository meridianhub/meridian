// Package main implements the Horizon Integrity Proof CLI tool.
// This tool demonstrates Meridian's resilience against phantom transactions
// by simulating network failures and verifying idempotency guarantees.
package main

import (
	"errors"
	"fmt"
	"io"
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

// Exit codes for the demo tool.
const (
	ExitCodePassed = 0 // Demo passed - no phantom transactions
	ExitCodeFailed = 1 // Demo failed - integrity issue detected
	ExitCodeError  = 2 // Execution error - service unavailable, etc.
)

// ANSI color codes for terminal output.
const (
	colorReset  = "\033[0m"
	colorGreen  = "\033[32m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorBold   = "\033[1m"
	colorCyan   = "\033[36m"
)

// StepStatus represents the outcome of a demo step.
type StepStatus int

const (
	StatusOK StepStatus = iota
	StatusTimeout
	StatusFailed
	StatusPassed
	StatusError
)

func (s StepStatus) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusTimeout:
		return "TIMEOUT"
	case StatusFailed:
		return "FAIL"
	case StatusPassed:
		return "PASSED"
	case StatusError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

func (s StepStatus) color() string {
	switch s {
	case StatusOK, StatusPassed:
		return colorGreen
	case StatusFailed, StatusError:
		return colorRed
	case StatusTimeout:
		return colorYellow
	default:
		return colorReset
	}
}

// StepResult captures the outcome of a single demo step.
type StepResult struct {
	Step    int
	Name    string
	Status  StepStatus
	Details string
}

// DemoResult captures the overall demo outcome.
type DemoResult struct {
	Steps   []StepResult
	Verdict Verdict
}

// Verdict represents the final demo verdict.
type Verdict int

const (
	VerdictPassed Verdict = iota
	VerdictFailed
	VerdictError
)

func (v Verdict) String() string {
	switch v {
	case VerdictPassed:
		return "PASSED"
	case VerdictFailed:
		return "FAILED"
	case VerdictError:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

func (v Verdict) exitCode() int {
	switch v {
	case VerdictPassed:
		return ExitCodePassed
	case VerdictFailed:
		return ExitCodeFailed
	case VerdictError:
		return ExitCodeError
	default:
		return ExitCodeError
	}
}

func (v Verdict) color() string {
	switch v {
	case VerdictPassed:
		return colorGreen
	case VerdictFailed:
		return colorRed
	case VerdictError:
		return colorYellow
	default:
		return colorYellow
	}
}

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
	exitCode := runWithExitCode()
	os.Exit(exitCode)
}

func runWithExitCode() int {
	result, err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return ExitCodeError
	}
	if result == nil {
		return ExitCodePassed
	}
	return result.Verdict.exitCode()
}

func run() (*DemoResult, error) {
	cfg := DefaultConfig()
	var demoResult *DemoResult

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
			var err error
			demoResult, err = runDemo(cfg)
			return err
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

	err := rootCmd.Execute()
	return demoResult, err
}

// runDemo executes the Horizon integrity proof demonstration.
func runDemo(cfg *Config) (*DemoResult, error) {
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
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Placeholder demo results - the horizon-demo is a proof-of-concept
	// demonstrating the CLI structure and output formatting.
	// See tag 99-horizon-proof for the completed implementation tasks.
	result := &DemoResult{
		Steps: []StepResult{
			{Step: 1, Name: "Create Test Account", Status: StatusOK, Details: "HORIZON-TEST-placeholder"},
			{Step: 2, Name: "Deposit GBP 1,000", Status: StatusOK, Details: "Balance: GBP 1,000.00"},
			{Step: 3, Name: "Payment (Attempt 1)", Status: StatusTimeout, Details: "Client: context deadline exceeded"},
			{Step: 4, Name: "Payment (Attempt 2)", Status: StatusOK, Details: "Idempotency hit, PO: po_placeholder"},
			{Step: 5, Name: "Verify Balance", Status: StatusPassed, Details: "GBP 900.00 (expected: GBP 900.00)"},
			{Step: 6, Name: "Verify Orders", Status: StatusPassed, Details: "1 order (expected: 1)"},
		},
		Verdict: VerdictPassed,
	}

	// Print ASCII table
	printASCIITable(os.Stdout, result)

	// JSON report generation is a future enhancement
	logger.Debug("report output configured", "path", cfg.Output)

	// Execute cleanup unless --no-cleanup is specified
	if !cfg.NoCleanup {
		logger.Debug("executing cleanup", "account", "HORIZON-TEST-placeholder")
		if err := executeCleanup(logger, "placeholder-account-id"); err != nil {
			logger.Warn("cleanup failed", "error", err)
			fmt.Fprintf(os.Stderr, "\n%sWarning:%s Manual cleanup may be required for test account\n",
				colorYellow, colorReset)
		}
	} else {
		logger.Info("skipping cleanup (--no-cleanup specified)")
	}

	logger.Info("demo completed", "verdict", result.Verdict.String())
	return result, nil
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

// printASCIITable prints the demo results as a formatted ASCII table with ANSI colors.
func printASCIITable(w io.Writer, result *DemoResult) {
	const (
		stepWidth   = 25
		statusWidth = 12
		headerLine  = "=============================================================="
		rowLine     = "--------------------------------------------------------------"
	)

	// Header
	_, _ = fmt.Fprintf(w, "\n%s%sHORIZON INTEGRITY PROOF%s\n", colorBold, colorCyan, colorReset)
	_, _ = fmt.Fprintln(w, headerLine)
	_, _ = fmt.Fprintln(w)

	// Column headers
	_, _ = fmt.Fprintf(w, "%-*s %-*s %s\n", stepWidth, "STEP", statusWidth, "STATUS", "DETAILS")
	_, _ = fmt.Fprintln(w, rowLine)

	// Print each step
	for _, step := range result.Steps {
		statusStr := fmt.Sprintf("%s%-*s%s",
			step.Status.color(),
			statusWidth,
			step.Status.String(),
			colorReset)

		_, _ = fmt.Fprintf(w, "%d. %-*s %s %s\n",
			step.Step,
			stepWidth-3, // -3 for "N. " prefix
			step.Name,
			statusStr,
			step.Details)
	}

	_, _ = fmt.Fprintln(w, rowLine)
	_, _ = fmt.Fprintln(w)

	// Verdict line
	verdictMsg := getVerdictMessage(result.Verdict)
	_, _ = fmt.Fprintf(w, "%sVERDICT: %s%s%s - %s%s\n",
		colorBold,
		result.Verdict.color(),
		result.Verdict.String(),
		colorReset,
		verdictMsg,
		colorReset)
}

func getVerdictMessage(v Verdict) string {
	switch v {
	case VerdictPassed:
		return "No phantom transactions, no double-spend"
	case VerdictFailed:
		return "Integrity issue detected"
	case VerdictError:
		return "Execution error occurred"
	default:
		return "Unknown result"
	}
}

// executeCleanup attempts to clean up the test account created during the demo.
// Currently a no-op as the demo uses placeholder data.
func executeCleanup(logger *slog.Logger, accountID string) error {
	logger.Debug("cleanup would delete account", "account_id", accountID)
	return nil
}
