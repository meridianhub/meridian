package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// Export command errors.
var (
	// ErrInvalidDateFormat is returned when a date string cannot be parsed.
	ErrInvalidDateFormat = errors.New("invalid date format, expected YYYY-MM-DD or RFC3339")
	// ErrDateRangeInvalid is returned when --from is after --to.
	ErrDateRangeInvalid = errors.New("--from date must be before --to date")
)

// Export command flags.
var (
	exportOutput     string
	exportInstrument string
	exportFrom       string
	exportTo         string
	exportAccountID  string
)

// exportCmd represents the export command.
var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export positions to a CSV file",
	Long: `Export positions from the Meridian platform to a CSV file.

The export command reads positions from the database and writes them to a CSV
file. It supports filtering by instrument, date range, and account.

Features:
  - Filtering by instrument code
  - Date range filtering (positions valid during the range)
  - Account-specific exports
  - Progress reporting for large exports
  - Streaming output for memory efficiency

CSV Output Format:
  The CSV will include headers and the following columns:
  - instrument_code: The instrument code
  - version: Instrument version
  - bucket_id: The bucket identifier
  - amount: Position amount
  - account_id: Account identifier
  - valid_from: Validity start time (RFC3339)
  - valid_to: Validity end time (RFC3339)
  - created_at: Record creation timestamp
  - attr_*: Attribute columns (dynamic based on instrument)

Exit Codes:
  0 - Success
  1 - Failure (error occurred)

Examples:
  # Export all positions for a tenant
  position-tool export --tenant=acme_bank --output=all_positions.csv

  # Export positions for a specific instrument
  position-tool export --tenant=acme_bank --instrument=USD --output=usd_positions.csv

  # Export positions for a date range
  position-tool export --tenant=acme_bank --from=2024-01-01 --to=2024-12-31 --output=2024.csv

  # Export positions for a specific account
  position-tool export --tenant=acme_bank --account-id=acc-123 --output=account.csv

  # Combined filtering
  position-tool export --tenant=acme_bank \
    --instrument=CARBON_CREDIT \
    --from=2024-01-01 \
    --account-id=acc-456 \
    --output=carbon_2024.csv`,
	RunE:          runExportWrapper,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(exportCmd)

	exportCmd.Flags().StringVar(&exportOutput, "output", "",
		"Path to output CSV file (required)")
	exportCmd.Flags().StringVar(&exportInstrument, "instrument", "",
		"Filter by instrument code")
	exportCmd.Flags().StringVar(&exportFrom, "from", "",
		"Filter positions valid from this date (YYYY-MM-DD or RFC3339)")
	exportCmd.Flags().StringVar(&exportTo, "to", "",
		"Filter positions valid to this date (YYYY-MM-DD or RFC3339)")
	exportCmd.Flags().StringVar(&exportAccountID, "account-id", "",
		"Filter by account ID")

	_ = exportCmd.MarkFlagRequired("output")
}

// runExportWrapper handles exit codes for the export command.
func runExportWrapper(cmd *cobra.Command, args []string) error {
	err := runExport(cmd, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return nil
}

func runExport(_ *cobra.Command, _ []string) error {
	// Validate required flags
	if err := validateCommonFlags(); err != nil {
		return err
	}

	// Parse date filters if provided
	var fromTime, toTime *time.Time
	if exportFrom != "" {
		t, err := parseDate(exportFrom)
		if err != nil {
			return fmt.Errorf("invalid --from date: %w", err)
		}
		fromTime = &t
	}
	if exportTo != "" {
		t, err := parseDate(exportTo)
		if err != nil {
			return fmt.Errorf("invalid --to date: %w", err)
		}
		toTime = &t
	}

	// Validate date range
	if fromTime != nil && toTime != nil && fromTime.After(*toTime) {
		return ErrDateRangeInvalid
	}

	// Set up graceful shutdown context
	ctx, cancel := ShutdownContext()
	defer cancel()

	// Log configuration
	logger := slog.Default()
	logger.Info("starting export",
		"tenant", tenantID,
		"output", exportOutput,
		"instrument", exportInstrument,
		"from", exportFrom,
		"to", exportTo,
		"account_id", exportAccountID,
		"dry_run", dryRun,
	)

	start := time.Now()

	// Execute export
	result, err := executeExport(ctx, &exportConfig{
		TenantID:   tenantID,
		Output:     exportOutput,
		Instrument: exportInstrument,
		From:       fromTime,
		To:         toTime,
		AccountID:  exportAccountID,
		DryRun:     dryRun,
		DBUrl:      dbURL,
	})
	if err != nil {
		return err
	}

	elapsed := time.Since(start)

	// Print results
	printExportResult(result, elapsed)

	return nil
}

// parseDate parses a date string in YYYY-MM-DD or RFC3339 format.
func parseDate(s string) (time.Time, error) {
	// Try RFC3339 first
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}

	// Try date-only format
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("%w: got %q", ErrInvalidDateFormat, s)
}

// exportConfig holds the configuration for an export operation.
type exportConfig struct {
	TenantID   string
	Output     string
	Instrument string
	From       *time.Time
	To         *time.Time
	AccountID  string
	DryRun     bool
	DBUrl      string
}

// exportResult holds the results of an export operation.
type exportResult struct {
	TotalRows      int64
	OutputFile     string
	FileSizeBytes  int64
	Interrupted    bool
	InterruptedRow int64
}

// executeExport performs the actual export operation.
// TODO: Implement full export logic in a future subtask
func executeExport(ctx context.Context, cfg *exportConfig) (*exportResult, error) {
	logger := slog.Default()

	// Placeholder implementation
	if cfg.DryRun {
		logger.Info("dry-run mode: would export positions",
			"output", cfg.Output,
			"tenant", cfg.TenantID,
		)

		return &exportResult{
			TotalRows:  0, // Would be actual count from query
			OutputFile: cfg.Output,
		}, nil
	}

	// Check for context cancellation
	select {
	case <-ctx.Done():
		logger.Info("export interrupted")
		return &exportResult{
			Interrupted:    true,
			InterruptedRow: 0,
			OutputFile:     cfg.Output,
		}, nil
	default:
	}

	return &exportResult{
		TotalRows:  0,
		OutputFile: cfg.Output,
	}, nil
}

// printExportResult outputs the export results in a formatted report.
func printExportResult(result *exportResult, elapsed time.Duration) {
	fmt.Println()
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println("|                         EXPORT SUMMARY                                    |")
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println()

	if dryRun {
		fmt.Println("  Mode:              DRY-RUN (no file written)")
	} else {
		fmt.Println("  Mode:              LIVE")
	}

	fmt.Printf("  Output File:       %s\n", result.OutputFile)
	fmt.Printf("  Duration:          %s\n", formatDuration(elapsed))
	fmt.Println()

	fmt.Println("  Results:")
	fmt.Printf("    Total Rows:      %d\n", result.TotalRows)
	if result.FileSizeBytes > 0 {
		fmt.Printf("    File Size:       %s\n", formatBytes(result.FileSizeBytes))
	}
	fmt.Println()

	if result.Interrupted {
		fmt.Println("  STATUS: INTERRUPTED")
		fmt.Printf("  Stopped at row:    %d\n", result.InterruptedRow)
	} else if dryRun {
		fmt.Println("  STATUS: DRY-RUN COMPLETE")
	} else {
		fmt.Println("  STATUS: SUCCESS")
	}

	fmt.Println()
	fmt.Println("+---------------------------------------------------------------------------+")
}

// formatBytes formats a byte count for human-readable display.
func formatBytes(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)

	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.2f GB", float64(bytes)/GB)
	case bytes >= MB:
		return fmt.Sprintf("%.2f MB", float64(bytes)/MB)
	case bytes >= KB:
		return fmt.Sprintf("%.2f KB", float64(bytes)/KB)
	default:
		return fmt.Sprintf("%d bytes", bytes)
	}
}
