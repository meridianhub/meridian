package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// Import command errors.
var (
	// ErrSourceNotFound is returned when the source file does not exist.
	ErrSourceNotFound = errors.New("source file not found")
	// ErrImportValidationFailed is returned when import completes with validation errors.
	ErrImportValidationFailed = errors.New("import completed with validation errors")
)

// Import command flags.
var (
	importSource            string
	importBatchSize         int
	importCreateInstruments bool
	importResumeFrom        string
)

// importCmd represents the import command.
var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import positions from a CSV file",
	Long: `Import positions from a CSV file into the Meridian platform.

The import command reads a CSV file containing position data and imports it
into the position-keeping database. It supports:

  - Batch processing for efficient database operations
  - Validation of all rows before persisting
  - Progress reporting with estimated time remaining
  - Resume capability for interrupted imports
  - Dry-run mode for validation without persistence
  - Optional auto-creation of missing instruments

CSV Format:
  The CSV must have headers matching the position schema. Required columns:
  - instrument_code: The instrument code (e.g., USD, CARBON_CREDIT)
  - amount: The position amount (decimal)
  - account_id: The account identifier (UUID)

  Optional columns:
  - valid_from: Validity start time (RFC3339)
  - valid_to: Validity end time (RFC3339)
  - attr_*: Attribute columns (e.g., attr_vintage_year, attr_registry)

  Note: bucket_key is NOT imported directly - it is computed from attributes using
  the instrument's fungibility key expression (CEL).

Exit Codes:
  0 - Success (all rows imported or dry-run validation passed)
  1 - Failure (validation errors or import failed)

Examples:
  # Basic import
  position-tool import --tenant=acme_bank --source=positions.csv --db-url=postgres://...

  # Dry-run to validate CSV without importing
  position-tool import --tenant=acme_bank --source=positions.csv --dry-run

  # Import with larger batch size for better throughput
  position-tool import --tenant=acme_bank --source=positions.csv --batch-size=1000

  # Import and auto-create missing instruments
  position-tool import --tenant=acme_bank --source=positions.csv --create-instruments

  # Resume interrupted import from a checkpoint
  position-tool import --tenant=acme_bank --source=positions.csv --resume-from=<manifest-uuid>`,
	RunE:          runImportWrapper,
	SilenceErrors: true, // We handle errors ourselves for proper exit codes
}

func init() {
	rootCmd.AddCommand(importCmd)

	importCmd.Flags().StringVar(&importSource, "source", "",
		"Path to CSV file to import (required)")
	importCmd.Flags().IntVar(&importBatchSize, "batch-size", 500,
		"Number of rows to process per batch (default 500)")
	importCmd.Flags().BoolVar(&importCreateInstruments, "create-instruments", false,
		"Auto-create missing instruments during import")
	importCmd.Flags().StringVar(&importResumeFrom, "resume-from", "",
		"Manifest UUID to resume an interrupted import from")

	_ = importCmd.MarkFlagRequired("source")
}

// runImportWrapper handles exit codes for the import command.
func runImportWrapper(cmd *cobra.Command, args []string) error {
	err := runImport(cmd, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return nil
}

func runImport(_ *cobra.Command, _ []string) error {
	// Validate required flags
	if err := validateCommonFlags(); err != nil {
		return err
	}

	// Validate source file exists
	if _, err := os.Stat(importSource); os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", ErrSourceNotFound, importSource)
	}

	// Parse resume-from UUID if provided
	var resumeManifestID *uuid.UUID
	if importResumeFrom != "" {
		id, err := uuid.Parse(importResumeFrom)
		if err != nil {
			return fmt.Errorf("invalid resume-from UUID: %w", err)
		}
		resumeManifestID = &id
	}

	// Set up graceful shutdown context
	ctx, cancel := ShutdownContext()
	defer cancel()

	// Log configuration
	logger := slog.Default()
	logger.Info("starting import",
		"tenant", tenantID,
		"source", importSource,
		"batch_size", importBatchSize,
		"dry_run", dryRun,
		"create_instruments", importCreateInstruments,
		"resume_from", importResumeFrom,
	)

	start := time.Now()

	// Execute import
	result, err := executeImport(ctx, &importConfig{
		TenantID:          tenantID,
		Source:            importSource,
		BatchSize:         importBatchSize,
		DryRun:            dryRun,
		CreateInstruments: importCreateInstruments,
		ResumeManifestID:  resumeManifestID,
		DBUrl:             dbURL,
	})
	if err != nil {
		return err
	}

	elapsed := time.Since(start)

	// Print results
	printImportResult(result, elapsed)

	// Return error if there were validation failures
	if result.ValidationErrors > 0 {
		return fmt.Errorf("%w: %d errors", ErrImportValidationFailed, result.ValidationErrors)
	}

	return nil
}

// importConfig holds the configuration for an import operation.
type importConfig struct {
	TenantID          string
	Source            string
	BatchSize         int
	DryRun            bool
	CreateInstruments bool
	ResumeManifestID  *uuid.UUID
	DBUrl             string
}

// importResult holds the results of an import operation.
type importResult struct {
	TotalRows        int64
	ImportedRows     int64
	SkippedRows      int64
	ValidationErrors int64
	ManifestID       uuid.UUID
	CheckpointRow    int64
	Interrupted      bool
}

// executeImport performs the actual import operation.
// TODO: Implement full import logic in subtask 36.6
func executeImport(ctx context.Context, cfg *importConfig) (*importResult, error) {
	logger := slog.Default()

	// Placeholder implementation - will be fully implemented in subtask 36.6
	if cfg.DryRun {
		logger.Info("dry-run mode: would validate and import positions",
			"source", cfg.Source,
			"tenant", cfg.TenantID,
		)

		// Simulate counting rows for dry-run output
		return &importResult{
			TotalRows:        0, // Would be actual count from CSV
			ImportedRows:     0,
			SkippedRows:      0,
			ValidationErrors: 0,
			ManifestID:       uuid.New(),
		}, nil
	}

	// Check for context cancellation (graceful shutdown)
	select {
	case <-ctx.Done():
		logger.Info("import interrupted, saving checkpoint")
		return &importResult{
			Interrupted:   true,
			CheckpointRow: 0, // Would be actual checkpoint
			ManifestID:    uuid.New(),
		}, nil
	default:
	}

	return &importResult{
		TotalRows:        0,
		ImportedRows:     0,
		SkippedRows:      0,
		ValidationErrors: 0,
		ManifestID:       uuid.New(),
	}, nil
}

// printImportResult outputs the import results in a formatted report.
func printImportResult(result *importResult, elapsed time.Duration) {
	fmt.Println()
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println("|                         IMPORT SUMMARY                                    |")
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println()

	if dryRun {
		fmt.Println("  Mode:              DRY-RUN (no changes persisted)")
	} else {
		fmt.Println("  Mode:              LIVE")
	}

	fmt.Printf("  Manifest ID:       %s\n", result.ManifestID)
	fmt.Printf("  Duration:          %s\n", formatDuration(elapsed))
	fmt.Println()

	fmt.Println("  Results:")
	fmt.Printf("    Total Rows:      %d\n", result.TotalRows)
	fmt.Printf("    Imported:        %d\n", result.ImportedRows)
	fmt.Printf("    Skipped:         %d\n", result.SkippedRows)
	fmt.Printf("    Errors:          %d\n", result.ValidationErrors)
	fmt.Println()

	if result.Interrupted {
		fmt.Println("  STATUS: INTERRUPTED")
		fmt.Printf("  Checkpoint at row: %d\n", result.CheckpointRow)
		fmt.Printf("  Resume with: --resume-from=%s\n", result.ManifestID)
	} else if result.ValidationErrors > 0 {
		fmt.Println("  STATUS: COMPLETED WITH ERRORS")
	} else if dryRun {
		fmt.Println("  STATUS: VALIDATION PASSED")
	} else {
		fmt.Println("  STATUS: SUCCESS")
	}

	fmt.Println()
	fmt.Println("+---------------------------------------------------------------------------+")
}
