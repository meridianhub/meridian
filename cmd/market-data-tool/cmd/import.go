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

	csvadapter "github.com/meridianhub/meridian/cmd/market-data-tool/internal/adapters/csv"
	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/checkpoint"
	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/infra"
	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/validation"
)

// Import command errors.
var (
	// ErrSourceNotFound is returned when the source file does not exist.
	ErrSourceNotFound = errors.New("source file not found")
	// ErrImportValidationFailed is returned when import completes with validation errors.
	ErrImportValidationFailed = errors.New("import completed with validation errors")
	// ErrDatasetRequired is returned when --dataset flag is missing.
	ErrDatasetRequired = errors.New("--dataset is required")
	// ErrSourceCodeRequired is returned when --source-code flag is missing.
	ErrSourceCodeRequired = errors.New("--source-code is required")
	// ErrDBURLRequired is returned when --db-url flag is missing and DATABASE_URL is not set.
	ErrDBURLRequired = errors.New("--db-url is required for checkpoint persistence (or set DATABASE_URL environment variable)")
	// ErrSourceIDNotSupported is returned when --source-id is used instead of --source-code.
	ErrSourceIDNotSupported = errors.New("--source-id is not supported; use --source-code instead (e.g., --source-code=BLOOMBERG)")
)

// Import command flags.
var (
	importSource     string
	importDataset    string
	importSourceCode string
	importSourceID   string
	importBatchSize  int
	importResumeFrom string
	dbURL            string
)

// importCmd represents the import command.
var importCmd = &cobra.Command{
	Use:   "import",
	Short: "Import observations from a CSV file",
	Long: `Import market data observations from a CSV file into the Meridian platform.

The import command reads a CSV file containing observation data and sends it
to the Market Information Service via gRPC. It supports:

  - Batch processing for efficient gRPC operations
  - Validation preview before persisting
  - Progress reporting with estimated time remaining
  - Resume capability for interrupted imports
  - Dry-run mode for validation without persistence
  - Dynamic attribute extraction based on dataset schema

CSV Format:
  The CSV must have headers matching the observation schema. Required columns:
  - observed_at: When the observation was made (RFC3339 timestamp)
  - quality_level: ESTIMATE, PROVISIONAL, ACTUAL, or REVISED
  - value: The observed value (decimal string, max 64 chars)

  Optional columns:
  - valid_from: When the observation becomes valid (RFC3339)
  - valid_to: When the observation expires (RFC3339)

  Additional columns are extracted as attributes based on the dataset's
  attribute_schema definition.

Causation ID Format:
  Each import generates a causation ID for traceability:
  import-{tenant_id}-{manifest_id}

Exit Codes:
  0 - Success (all rows imported or dry-run validation passed)
  1 - Failure (validation errors or import failed)

Examples:
  # Basic import
  market-data-tool import --tenant=acme_corp --source=rates.csv --dataset=USD_EUR_FX --source-code=BLOOMBERG

  # Dry-run to validate CSV without importing
  market-data-tool import --tenant=acme_corp --source=rates.csv --dataset=USD_EUR_FX --source-code=BLOOMBERG --dry-run

  # Import with larger batch size for better throughput
  market-data-tool import --tenant=acme_corp --source=rates.csv --dataset=USD_EUR_FX --source-code=BLOOMBERG --batch-size=1000

  # Import using source ID instead of code
  market-data-tool import --tenant=acme_corp --source=rates.csv --dataset=USD_EUR_FX --source-id=550e8400-e29b-41d4-a716-446655440000

  # Resume interrupted import from a checkpoint
  market-data-tool import --tenant=acme_corp --source=rates.csv --dataset=USD_EUR_FX --source-code=BLOOMBERG --resume-from=<manifest-uuid>`,
	RunE:          runImportWrapper,
	SilenceErrors: true, // We handle errors ourselves for proper exit codes
}

func init() {
	rootCmd.AddCommand(importCmd)

	importCmd.Flags().StringVar(&importSource, "source", "",
		"Path to CSV file to import (required)")
	importCmd.Flags().StringVar(&importDataset, "dataset", "",
		"Dataset code to import into (required)")
	importCmd.Flags().StringVar(&importSourceCode, "source-code", "",
		"Data source code (e.g., BLOOMBERG, REUTERS)")
	importCmd.Flags().StringVar(&importSourceID, "source-id", "",
		"Data source UUID (alternative to --source-code)")
	importCmd.Flags().IntVar(&importBatchSize, "batch-size", 500,
		"Number of rows to process per batch (default 500)")
	importCmd.Flags().StringVar(&importResumeFrom, "resume-from", "",
		"Manifest UUID to resume an interrupted import from")
	importCmd.Flags().StringVar(&dbURL, "db-url",
		getEnvOrDefault("DATABASE_URL", ""),
		"Database connection URL for checkpoint persistence (required unless set via DATABASE_URL env)")

	_ = importCmd.MarkFlagRequired("source")
	_ = importCmd.MarkFlagRequired("dataset")
}

// runImportWrapper handles errors for the import command.
func runImportWrapper(cmd *cobra.Command, args []string) error {
	return runImport(cmd, args)
}

func runImport(_ *cobra.Command, _ []string) error {
	// Validate required flags
	if err := validateCommonFlags(); err != nil {
		return err
	}

	if importDataset == "" {
		return ErrDatasetRequired
	}

	if importSourceCode == "" && importSourceID == "" {
		return ErrSourceCodeRequired
	}

	if dbURL == "" {
		return ErrDBURLRequired
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
		"dataset", importDataset,
		"source_code", importSourceCode,
		"source_id", importSourceID,
		"batch_size", importBatchSize,
		"dry_run", dryRun,
		"resume_from", importResumeFrom,
		"grpc_endpoint", grpcEndpoint,
	)

	start := time.Now()

	// Execute import
	result, err := executeImport(ctx, &importConfig{
		TenantID:         tenantID,
		Source:           importSource,
		Dataset:          importDataset,
		SourceCode:       importSourceCode,
		SourceID:         importSourceID,
		BatchSize:        importBatchSize,
		DryRun:           dryRun,
		ResumeManifestID: resumeManifestID,
		GRPCEndpoint:     grpcEndpoint,
		DBUrl:            dbURL,
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
	TenantID         string
	Source           string
	Dataset          string
	SourceCode       string
	SourceID         string
	BatchSize        int
	DryRun           bool
	ResumeManifestID *uuid.UUID
	GRPCEndpoint     string
	DBUrl            string
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
	CausationID      string
}

// executeImport performs the actual import operation.
func executeImport(ctx context.Context, cfg *importConfig) (*importResult, error) {
	logger := slog.Default()

	// Create gRPC client
	grpcClient, cleanup, err := infra.NewGRPCClient(ctx, infra.GRPCClientConfig{
		Endpoint: cfg.GRPCEndpoint,
		TenantID: cfg.TenantID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC client: %w", err)
	}
	defer cleanup()

	// Determine source code for observations.
	// The proto expects source_code (e.g., "BLOOMBERG"), not UUID.
	// If --source-id was provided, validate it exists via list and get its code.
	sourceCode := cfg.SourceCode
	if sourceCode == "" && cfg.SourceID != "" {
		// User provided UUID, but the API expects source code
		return nil, ErrSourceIDNotSupported
	}

	// Validate the source code exists
	if sourceCode != "" {
		if _, err := grpcClient.ResolveSourceID(ctx, sourceCode); err != nil {
			return nil, fmt.Errorf("failed to validate source code %q: %w", sourceCode, err)
		}
	}

	// Fetch dataset definition for schema
	dataset, err := grpcClient.GetDataSet(ctx, cfg.Dataset, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dataset definition: %w", err)
	}

	// Create checkpoint manager
	checkpointMgr, err := infra.NewCheckpointManager(ctx, cfg.DBUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to create checkpoint manager: %w", err)
	}
	defer checkpointMgr.Close()

	// Start or resume checkpoint
	var cp *checkpoint.Checkpoint
	if cfg.ResumeManifestID != nil {
		cp, err = checkpointMgr.ResumeByID(ctx, *cfg.ResumeManifestID)
		if err != nil {
			return nil, fmt.Errorf("failed to resume checkpoint: %w", err)
		}
		logger.Info("resuming import from checkpoint",
			"manifest_id", cp.ManifestID,
			"processed_rows", cp.ProcessedRows,
		)
	} else {
		cp, err = checkpointMgr.StartImport(ctx, cfg.TenantID, cfg.Source)
		if err != nil {
			return nil, fmt.Errorf("failed to start import: %w", err)
		}
	}

	// Generate causation ID
	causationID := fmt.Sprintf("import-%s-%s", cfg.TenantID, cp.ManifestID)

	// Dry-run mode: validate without persisting
	if cfg.DryRun {
		return executeDryRun(ctx, cfg, cp, grpcClient, dataset, logger)
	}

	// Execute live import with checkpoint support
	return executeLiveImport(ctx, cfg, cp, checkpointMgr, grpcClient, dataset, sourceCode, causationID, logger)
}

// executeDryRun validates the CSV without persisting observations.
func executeDryRun(
	ctx context.Context,
	cfg *importConfig,
	cp *checkpoint.Checkpoint,
	grpcClient *infra.GRPCClient,
	dataset *infra.DataSetDefinition,
	logger *slog.Logger,
) (*importResult, error) {
	result := &importResult{
		ManifestID:  cp.ManifestID,
		CausationID: fmt.Sprintf("import-%s-%s", cfg.TenantID, cp.ManifestID),
	}

	// Open CSV file
	file, err := os.Open(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to open source file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Create validation pipeline
	pipeline := validation.NewPipeline(validation.PipelineConfig{
		DatasetChecker:  validation.NewDatasetChecker(grpcClient, cfg.Dataset),
		SchemaValidator: validation.NewSchemaValidator(dataset.AttributeSchema),
		CELPreview:      infra.NewCELPreview(dataset.ValidationExpression),
		Logger:          logger,
	})

	// Create CSV parser
	csvParser := csvadapter.NewParser(dataset)

	// Parse and validate CSV
	parseConfig := csvadapter.ParseConfig{
		BatchSize:     cfg.BatchSize,
		SkipEmptyRows: true,
	}

	parseResult, err := csvParser.Parse(ctx, file, parseConfig, func(batch csvadapter.RowBatch) error {
		// Validate each row in the batch
		for _, csvRow := range batch.Rows {
			validationRow := csvRowToValidationRow(&csvRow, cfg.Dataset)
			rowErr := pipeline.ValidateRow(ctx, validationRow)
			if rowErr != nil && rowErr.HasErrors() {
				result.ValidationErrors++
			} else {
				result.ImportedRows++
			}
		}
		// Count parse errors from this batch
		result.ValidationErrors += int64(len(batch.Errors))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("CSV parsing failed: %w", err)
	}

	result.TotalRows = int64(parseResult.RowCount) + int64(parseResult.ErrorCount)

	logger.Info("dry-run validation complete",
		"total_rows", result.TotalRows,
		"valid_rows", result.ImportedRows,
		"validation_errors", result.ValidationErrors,
	)

	return result, nil
}

// executeLiveImport performs the actual import with checkpoint persistence.
//
//nolint:gocognit,gocyclo // complexity is acceptable for this import orchestration function
func executeLiveImport(
	ctx context.Context,
	cfg *importConfig,
	cp *checkpoint.Checkpoint,
	checkpointMgr *infra.CheckpointManagerAdapter,
	grpcClient *infra.GRPCClient,
	dataset *infra.DataSetDefinition,
	sourceCode string,
	causationID string,
	logger *slog.Logger,
) (*importResult, error) {
	result := &importResult{
		ManifestID:  cp.ManifestID,
		CausationID: causationID,
	}

	// Open CSV file
	file, err := os.Open(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to open source file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Create batch inserter
	batchInserter := infra.NewBatchInserter(infra.BatchInserterConfig{
		Client:      grpcClient,
		BatchSize:   cfg.BatchSize,
		DatasetCode: cfg.Dataset,
		SourceCode:  sourceCode,
		OnBatchComplete: func(batchNum, observationsInBatch, totalInserted int) {
			logger.Debug("batch inserted", "batch", batchNum, "count", observationsInBatch, "total", totalInserted)
		},
	})

	// Create validation pipeline
	pipeline := validation.NewPipeline(validation.PipelineConfig{
		DatasetChecker:  validation.NewDatasetChecker(grpcClient, cfg.Dataset),
		SchemaValidator: validation.NewSchemaValidator(dataset.AttributeSchema),
		CELPreview:      infra.NewCELPreview(dataset.ValidationExpression),
		Logger:          logger,
	})

	// Create CSV parser
	csvParser := csvadapter.NewParser(dataset)

	// Process import
	parseConfig := csvadapter.ParseConfig{
		BatchSize:     cfg.BatchSize,
		SkipEmptyRows: true,
	}

	parseResult, parseErr := csvParser.Parse(ctx, file, parseConfig, func(batch csvadapter.RowBatch) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		for _, csvRow := range batch.Rows {
			// Skip rows before the resume point.
			// LineNumber is 1-indexed where line 1 is the header (line 2 is first data row).
			// ProcessedRows counts data rows processed (both successful and failed).
			// LastProcessedLine equals ProcessedRows (the count of processed data rows).
			// Example: If we've processed 100 data rows (lines 2-101), ProcessedRows=100.
			// To resume, skip lines 2-101: lineNumber <= ProcessedRows + 1 = 101.
			// The +1 accounts for the header being line 1.
			if cp.ProcessedRows > 0 && csvRow.LineNumber <= cp.LastProcessedLine+1 {
				continue
			}

			// Validate the row
			validationRow := csvRowToValidationRow(&csvRow, cfg.Dataset)
			if rowErr := pipeline.ValidateRow(ctx, validationRow); rowErr != nil && rowErr.HasErrors() {
				cp.IncrementFailure(1)
				result.ValidationErrors++
				continue
			}

			// Add to batch inserter
			if err := batchInserter.Add(ctx, csvRowToObservation(&csvRow, cfg.Dataset, sourceCode)); err != nil {
				return fmt.Errorf("failed to add observation to batch: %w", err)
			}

			cp.IncrementSuccess(1)
			result.ImportedRows++
		}

		for range batch.Errors {
			cp.IncrementFailure(1)
			result.ValidationErrors++
		}

		if updateErr := checkpointMgr.UpdateProgress(ctx, cp); updateErr != nil {
			logger.Warn("failed to update checkpoint", "error", updateErr)
		}

		return nil
	})

	// Handle parse result
	if errors.Is(parseErr, context.Canceled) {
		return handleImportInterrupt(ctx, batchInserter, cp, checkpointMgr, result, logger), nil
	}
	if parseErr != nil {
		if failErr := checkpointMgr.Fail(ctx, cp, parseErr); failErr != nil {
			logger.Warn("failed to mark checkpoint as failed", "error", failErr)
		}
		return nil, fmt.Errorf("import failed: %w", parseErr)
	}

	// Flush remaining batch
	if flushErr := batchInserter.Flush(ctx); flushErr != nil {
		if markErr := checkpointMgr.Fail(ctx, cp, flushErr); markErr != nil {
			logger.Warn("failed to mark checkpoint as failed", "error", markErr)
		}
		return nil, fmt.Errorf("failed to flush final batch: %w", flushErr)
	}

	cp.SetTotalRows(parseResult.RowCount + parseResult.ErrorCount)
	if completeErr := checkpointMgr.Complete(ctx, cp); completeErr != nil {
		logger.Warn("failed to complete checkpoint", "error", completeErr)
	}

	result.TotalRows = int64(parseResult.RowCount) + int64(parseResult.ErrorCount)
	logger.Info("import complete",
		"total_rows", result.TotalRows,
		"imported", result.ImportedRows,
		"errors", result.ValidationErrors,
		"manifest_id", result.ManifestID,
	)

	return result, nil
}

// handleImportInterrupt handles cleanup when import is interrupted.
func handleImportInterrupt(
	_ context.Context,
	batchInserter *infra.BatchInserter,
	cp *checkpoint.Checkpoint,
	checkpointMgr *infra.CheckpointManagerAdapter,
	result *importResult,
	logger *slog.Logger,
) *importResult {
	// Use fresh context for cleanup since the original may be canceled.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second) //nolint:contextcheck // fresh context for cleanup after signal/cancellation
	defer cancel()

	if flushErr := batchInserter.Flush(cleanupCtx); flushErr != nil { //nolint:contextcheck // uses cleanup context created above
		logger.Warn("failed to flush batch on interrupt", "error", flushErr)
	}
	if cancelErr := checkpointMgr.Cancel(cleanupCtx, cp); cancelErr != nil { //nolint:contextcheck // uses cleanup context created above
		logger.Warn("failed to save checkpoint on interrupt", "error", cancelErr)
	}
	result.Interrupted = true
	result.CheckpointRow = int64(cp.ProcessedRows)
	result.TotalRows = int64(cp.TotalRows)
	logger.Info("import interrupted, checkpoint saved",
		"checkpoint_row", cp.ProcessedRows,
		"manifest_id", cp.ManifestID,
	)
	return result
}

// csvRowToValidationRow converts a CSV row to a validation row.
func csvRowToValidationRow(csvRow *csvadapter.ObservationRow, datasetCode string) *validation.ObservationRow {
	return &validation.ObservationRow{
		LineNumber:   csvRow.LineNumber,
		DatasetCode:  datasetCode,
		Value:        csvRow.Value,
		ObservedAt:   csvRow.ObservedAt,
		ValidFrom:    csvRow.ValidFrom,
		ValidTo:      csvRow.ValidTo,
		QualityLevel: csvRow.QualityLevel,
		Attributes:   csvRow.Attributes,
	}
}

// csvRowToObservation converts a CSV row to a gRPC observation entry.
func csvRowToObservation(csvRow *csvadapter.ObservationRow, datasetCode, sourceCode string) *infra.ObservationEntry {
	return &infra.ObservationEntry{
		DatasetCode:     datasetCode,
		ObservedAt:      csvRow.ObservedAt,
		ValidFrom:       csvRow.ValidFrom,
		ValidTo:         csvRow.ValidTo,
		Value:           csvRow.Value,
		QualityLevel:    csvRow.QualityLevel,
		SourceCode:      sourceCode,
		Attributes:      csvRow.Attributes,
		ClientReference: fmt.Sprintf("line-%d", csvRow.LineNumber),
	}
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
	fmt.Printf("  Causation ID:      %s\n", result.CausationID)
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
