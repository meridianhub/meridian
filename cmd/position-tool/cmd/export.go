package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/exporter"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/infra"
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
	exportBatchSize  int
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
  - Date range filtering (positions created during the range)
  - Account-specific exports
  - Progress reporting for large exports
  - Streaming output for memory efficiency

CSV Output Format:
  The CSV will include headers and the following columns:
  - account_id: Account identifier
  - instrument_code: The instrument code
  - amount: Position amount
  - dimension: Asset dimension (Monetary, Energy, etc.)
  - created_at: Record creation timestamp (RFC3339)
  - reference_id: Reference to source event
  - attr_*: Attribute columns (dynamic based on position data)

  Note: bucket_key is NOT exported - it is computed from attributes using
  the instrument's fungibility key expression (CEL) during import.

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
		"Filter positions created from this date (YYYY-MM-DD or RFC3339)")
	exportCmd.Flags().StringVar(&exportTo, "to", "",
		"Filter positions created to this date (YYYY-MM-DD or RFC3339)")
	exportCmd.Flags().StringVar(&exportAccountID, "account-id", "",
		"Filter by account ID")
	exportCmd.Flags().IntVar(&exportBatchSize, "batch-size", exporter.DefaultBatchSize,
		"Number of rows to fetch per database query")

	_ = exportCmd.MarkFlagRequired("output")
}

// runExportWrapper handles exit codes for the export command.
func runExportWrapper(cmd *cobra.Command, args []string) error {
	err := runExport(cmd, args)
	if err != nil {
		// Don't print interrupted as an error
		if errors.Is(err, exporter.ErrExportInterrupted) {
			return nil
		}
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
		"batch_size", exportBatchSize,
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
		BatchSize:  exportBatchSize,
		DryRun:     dryRun,
		DBUrl:      dbURL,
	})

	elapsed := time.Since(start)

	// Handle interruption (still print results)
	if errors.Is(err, exporter.ErrExportInterrupted) {
		printExportResult(result, elapsed)
		return err
	}

	if err != nil {
		return err
	}

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
	BatchSize  int
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
	AttributeKeys  []string
}

// executeExport performs the actual export operation.
func executeExport(ctx context.Context, cfg *exportConfig) (*exportResult, error) {
	logger := slog.Default()

	// Create database connection pool
	pool, err := pgxpool.New(ctx, cfg.DBUrl)
	if err != nil {
		return nil, fmt.Errorf("connecting to database: %w", err)
	}
	defer pool.Close()

	// Create exporter
	exp, err := exporter.New(pool, logger)
	if err != nil {
		return nil, fmt.Errorf("creating exporter: %w", err)
	}

	// Set up progress tracker with console output
	tracker := infra.NewProgressTracker(infra.ProgressTrackerConfig{
		DryRun: cfg.DryRun,
		OnProgress: func(event infra.ProgressEvent) {
			switch event.Type {
			case infra.ProgressEventStarted:
				fmt.Printf("\n  Starting export: %s\n", event.Message)
			case infra.ProgressEventBatchComplete:
				// Calculate progress percentage
				var pct float64
				if event.TotalExpected > 0 {
					pct = float64(event.TotalProcessed) / float64(event.TotalExpected) * 100
				}
				fmt.Printf("\r  Progress: %d/%d rows (%.1f%%) - %s",
					event.TotalProcessed, event.TotalExpected, pct, formatDuration(event.Duration))
			case infra.ProgressEventComplete:
				fmt.Printf("\n  %s\n", event.Message)
			case infra.ProgressEventError:
				fmt.Printf("\n  Error: %v\n", event.Error)
			}
		},
	})

	// Build export options
	opts := exporter.ExportOptions{
		OutputPath:     cfg.Output,
		TenantID:       cfg.TenantID,
		InstrumentCode: cfg.Instrument,
		AccountID:      cfg.AccountID,
		FromTime:       cfg.From,
		ToTime:         cfg.To,
		BatchSize:      cfg.BatchSize,
		DryRun:         cfg.DryRun,
	}

	// Execute export
	result, err := exp.Export(ctx, opts, tracker)
	if err != nil && !errors.Is(err, exporter.ErrExportInterrupted) {
		return nil, err
	}

	// Convert to exportResult
	return &exportResult{
		TotalRows:      result.TotalRows,
		OutputFile:     result.OutputFile,
		FileSizeBytes:  result.FileSizeBytes,
		Interrupted:    result.Interrupted,
		InterruptedRow: result.InterruptedRow,
		AttributeKeys:  result.AttributeKeys,
	}, err
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
	if len(result.AttributeKeys) > 0 {
		fmt.Printf("    Attribute Cols:  %d\n", len(result.AttributeKeys))
	}
	fmt.Println()

	// Calculate and display throughput
	if elapsed.Seconds() > 0 && result.TotalRows > 0 {
		rowsPerSec := float64(result.TotalRows) / elapsed.Seconds()
		fmt.Printf("  Throughput:        %.0f rows/sec\n", rowsPerSec)
		fmt.Println()
	}

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
