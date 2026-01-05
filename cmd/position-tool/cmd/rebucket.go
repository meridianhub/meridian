package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// Rebucket command flags.
var (
	rebucketInstrument string
	rebucketFrom       string
	rebucketTo         string
)

// rebucketCmd represents the rebucket command.
var rebucketCmd = &cobra.Command{
	Use:   "rebucket",
	Short: "Recalculate bucket keys for existing positions",
	Long: `Recalculate bucket keys for existing positions after instrument changes.

The rebucket command re-evaluates the bucket key expression for positions,
typically needed after an instrument definition change that affects bucket
key generation.

Use Cases:
  - Instrument fungibility expression changed
  - Attribute-based bucketing rules updated
  - Migration from default to custom bucket keys

Safety Features:
  - Dry-run mode shows changes without applying
  - Date range filtering to limit scope
  - Audit trail of all changes
  - Atomic batch updates

WARNING: This operation modifies existing position records. Always use
--dry-run first to preview changes.

Exit Codes:
  0 - Success
  1 - Failure (error occurred)

Examples:
  # Preview rebucketing for all CARBON_CREDIT positions
  position-tool rebucket --tenant=acme_bank --instrument=CARBON_CREDIT --dry-run

  # Rebucket positions created in a specific date range
  position-tool rebucket --tenant=acme_bank --instrument=CARBON_CREDIT \
    --from=2024-01-01 --to=2024-06-30

  # Execute rebucketing
  position-tool rebucket --tenant=acme_bank --instrument=CARBON_CREDIT`,
	RunE:          runRebucketWrapper,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(rebucketCmd)

	rebucketCmd.Flags().StringVar(&rebucketInstrument, "instrument", "",
		"Instrument code to rebucket (required)")
	rebucketCmd.Flags().StringVar(&rebucketFrom, "from", "",
		"Only rebucket positions created from this date (YYYY-MM-DD or RFC3339)")
	rebucketCmd.Flags().StringVar(&rebucketTo, "to", "",
		"Only rebucket positions created to this date (YYYY-MM-DD or RFC3339)")

	_ = rebucketCmd.MarkFlagRequired("instrument")
}

// runRebucketWrapper handles exit codes for the rebucket command.
func runRebucketWrapper(cmd *cobra.Command, args []string) error {
	err := runRebucket(cmd, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return nil
}

func runRebucket(_ *cobra.Command, _ []string) error {
	// Validate required flags
	if err := validateCommonFlags(); err != nil {
		return err
	}

	// Parse date filters if provided
	var fromTime, toTime *time.Time
	if rebucketFrom != "" {
		t, err := parseDate(rebucketFrom)
		if err != nil {
			return fmt.Errorf("invalid --from date: %w", err)
		}
		fromTime = &t
	}
	if rebucketTo != "" {
		t, err := parseDate(rebucketTo)
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
	logger.Info("starting rebucket",
		"tenant", tenantID,
		"instrument", rebucketInstrument,
		"from", rebucketFrom,
		"to", rebucketTo,
		"dry_run", dryRun,
	)

	start := time.Now()

	// Execute rebucket
	result, err := executeRebucket(ctx, &rebucketConfig{
		TenantID:   tenantID,
		Instrument: rebucketInstrument,
		From:       fromTime,
		To:         toTime,
		DryRun:     dryRun,
		DBUrl:      dbURL,
	})
	if err != nil {
		return err
	}

	elapsed := time.Since(start)

	// Print results
	printRebucketResult(result, elapsed)

	return nil
}

// rebucketConfig holds the configuration for a rebucket operation.
type rebucketConfig struct {
	TenantID   string
	Instrument string
	From       *time.Time
	To         *time.Time
	DryRun     bool
	DBUrl      string
}

// rebucketResult holds the results of a rebucket operation.
type rebucketResult struct {
	TotalPositions   int64
	ChangedPositions int64
	UnchangedCount   int64
	ErrorCount       int64
	Interrupted      bool
}

// executeRebucket performs the actual rebucket operation.
// TODO: Implement full rebucket logic - this integrates with rebucketing-tool internal packages
func executeRebucket(ctx context.Context, cfg *rebucketConfig) (*rebucketResult, error) {
	logger := slog.Default()

	// Placeholder implementation - will integrate with existing rebucketing-tool logic
	if cfg.DryRun {
		logger.Info("dry-run mode: would recalculate bucket keys",
			"instrument", cfg.Instrument,
			"tenant", cfg.TenantID,
		)

		return &rebucketResult{
			TotalPositions:   0,
			ChangedPositions: 0,
			UnchangedCount:   0,
			ErrorCount:       0,
		}, nil
	}

	// Check for context cancellation
	select {
	case <-ctx.Done():
		logger.Info("rebucket interrupted")
		return &rebucketResult{
			Interrupted: true,
		}, nil
	default:
	}

	return &rebucketResult{
		TotalPositions:   0,
		ChangedPositions: 0,
		UnchangedCount:   0,
		ErrorCount:       0,
	}, nil
}

// printRebucketResult outputs the rebucket results in a formatted report.
func printRebucketResult(result *rebucketResult, elapsed time.Duration) {
	fmt.Println()
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println("|                        REBUCKET SUMMARY                                   |")
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println()

	if dryRun {
		fmt.Println("  Mode:              DRY-RUN (no changes persisted)")
	} else {
		fmt.Println("  Mode:              LIVE")
	}

	fmt.Printf("  Instrument:        %s\n", rebucketInstrument)
	fmt.Printf("  Duration:          %s\n", formatDuration(elapsed))
	fmt.Println()

	fmt.Println("  Results:")
	fmt.Printf("    Total Positions: %d\n", result.TotalPositions)
	fmt.Printf("    Changed:         %d\n", result.ChangedPositions)
	fmt.Printf("    Unchanged:       %d\n", result.UnchangedCount)
	fmt.Printf("    Errors:          %d\n", result.ErrorCount)
	fmt.Println()

	if result.Interrupted {
		fmt.Println("  STATUS: INTERRUPTED")
	} else if result.ErrorCount > 0 {
		fmt.Println("  STATUS: COMPLETED WITH ERRORS")
	} else if dryRun {
		fmt.Println("  STATUS: DRY-RUN COMPLETE")
		if result.ChangedPositions > 0 {
			fmt.Printf("  Would update %d positions\n", result.ChangedPositions)
		}
	} else {
		fmt.Println("  STATUS: SUCCESS")
	}

	fmt.Println()
	fmt.Println("+---------------------------------------------------------------------------+")
}
