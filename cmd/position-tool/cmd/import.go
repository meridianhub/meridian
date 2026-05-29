package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/spf13/cobra"

	csvadapter "github.com/meridianhub/meridian/cmd/position-tool/internal/adapters/csv"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/checkpoint"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/infra"
	"github.com/meridianhub/meridian/cmd/position-tool/internal/validation"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
)

// Import command errors.
var (
	// ErrSourceNotFound is returned when the source file does not exist.
	ErrSourceNotFound = errors.New("source file not found")
	// ErrImportValidationFailed is returned when import completes with validation errors.
	ErrImportValidationFailed = errors.New("import completed with validation errors")
	// ErrInstrumentNotFound is returned when an instrument lookup fails.
	ErrInstrumentNotFound = errors.New("instrument not found")
	// ErrOperationNotSupported is returned when an operation is not supported by the adapter.
	ErrOperationNotSupported = errors.New("operation not supported by instrument checker")
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
// It integrates CSV parsing, validation pipeline, and batch insertion with checkpoint persistence.
func executeImport(ctx context.Context, cfg *importConfig) (*importResult, error) {
	logger := slog.Default()

	// Set up database connection
	pool, err := pgxpool.New(ctx, cfg.DBUrl)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	defer pool.Close()

	// Create checkpoint manager
	checkpointMgr, err := checkpoint.NewManager(pool)
	if err != nil {
		return nil, fmt.Errorf("failed to create checkpoint manager: %w", err)
	}

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

	// Dry-run mode: validate without persisting
	if cfg.DryRun {
		return executeDryRun(ctx, cfg, cp, pool, logger)
	}

	// Execute live import with checkpoint support
	return executeLiveImport(ctx, cfg, cp, checkpointMgr, pool, logger)
}

// executeDryRun validates the CSV without persisting positions.
func executeDryRun(ctx context.Context, cfg *importConfig, cp *checkpoint.Checkpoint, _ *pgxpool.Pool, logger *slog.Logger) (*importResult, error) {
	result := &importResult{ManifestID: cp.ManifestID}

	// Open CSV file
	file, err := os.Open(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to open source file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Create instrument checker for gRPC-based instrument lookup
	instrumentChecker, err := validation.NewInstrumentChecker(ctx, validation.InstrumentCheckerConfig{
		Target: getEnvOrDefault("REFERENCE_DATA_URL", "localhost:50051"),
		Logger: logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create instrument checker: %w", err)
	}
	defer func() { _ = instrumentChecker.Close() }()

	// Create instrument registry adapter for CSV parser
	registry := &instrumentCheckerRegistry{checker: instrumentChecker}

	// Create CSV parser
	csvParser := csvadapter.NewParser(registry)

	// Set up validation pipeline (minimal for dry-run - no DB lookups needed)
	duplicateChecker := validation.NewDuplicateChecker(validation.DefaultBloomFilterConfig(), nil)

	pipeline, err := validation.NewPipeline(validation.PipelineConfig{
		DuplicateChecker:  duplicateChecker,
		InstrumentChecker: instrumentChecker,
		Logger:            logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create validation pipeline: %w", err)
	}
	defer func() { _ = pipeline.Close() }()

	// Parse and validate CSV
	parseConfig := csvadapter.ParseConfig{
		BatchSize:     cfg.BatchSize,
		SkipEmptyRows: true,
	}

	parseResult, err := csvParser.Parse(ctx, file, parseConfig, func(batch csvadapter.RowBatch) error {
		// Validate each row in the batch
		for _, csvRow := range batch.Rows {
			validationRow := csvRowToValidationRow(&csvRow)
			rowErr := pipeline.ValidateRow(ctx, validationRow)
			if rowErr != nil && rowErr.HasErrors() {
				result.ValidationErrors++
			} else {
				result.ImportedRows++
			}
		}
		// Parse errors are counted via parseResult.ErrorCount after parsing completes
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("CSV parsing failed: %w", err)
	}

	result.TotalRows = int64(parseResult.RowCount) + int64(parseResult.ErrorCount)
	result.ValidationErrors += int64(parseResult.ErrorCount)

	logger.Info("dry-run validation complete",
		"total_rows", result.TotalRows,
		"valid_rows", result.ImportedRows,
		"validation_errors", result.ValidationErrors,
	)

	return result, nil
}

// importProcessor encapsulates the state and dependencies for processing import rows.
// It reduces cognitive complexity by separating row processing from orchestration.
type importProcessor struct {
	cfg               *importConfig
	cp                *checkpoint.Checkpoint
	result            *importResult
	instrumentChecker *validation.InstrumentChecker
	celEval           *infra.CELEvaluator
	pipeline          *validation.Pipeline
	batchInserter     *infra.BatchInserter
	logger            *slog.Logger
	fungibilityExprs  map[string]string // keyed by instrument code
}

// processRow handles validation and insertion of a single CSV row.
// Returns an error only for fatal errors that should stop processing.
func (p *importProcessor) processRow(ctx context.Context, csvRow *csvadapter.ImportRow) error {
	// Skip rows already committed in a prior run when resuming.
	if p.cp.ShouldSkipResumedLine(csvRow.LineNumber) {
		return nil
	}

	// Get fungibility expression for this instrument (cached per instrument code)
	fungibilityExpr, ok := p.fungibilityExprs[csvRow.InstrumentCode]
	if !ok {
		if instResult, instErr := p.instrumentChecker.Check(ctx, csvRow.InstrumentCode, 0); instErr == nil && instResult.Definition != nil {
			fungibilityExpr = instResult.Definition.FungibilityKeyExpression
			p.fungibilityExprs[csvRow.InstrumentCode] = fungibilityExpr
		}
	}

	// Compute bucket key using CEL
	bucketKey, computed := p.tryComputeBucketKey(csvRow, fungibilityExpr)
	if !computed {
		return nil // Validation error already recorded
	}

	// Validate the row
	validationRow := csvRowToValidationRow(csvRow)
	validationRow.BucketKey = bucketKey
	if rowErr := p.pipeline.ValidateRow(ctx, validationRow); rowErr != nil && rowErr.HasErrors() {
		p.cp.IncrementFailure(1)
		p.result.ValidationErrors++
		return nil
	}

	// Create and insert position
	return p.createAndInsertPosition(ctx, csvRow, bucketKey)
}

// tryComputeBucketKey evaluates the CEL expression to generate the bucket key.
// Returns (bucketKey, true) on success, or ("", false) if evaluation fails.
func (p *importProcessor) tryComputeBucketKey(csvRow *csvadapter.ImportRow, fungibilityExpr string) (string, bool) {
	if fungibilityExpr == "" {
		return "", true
	}
	bucketKey, err := p.celEval.EvaluateBucketKey(fungibilityExpr, csvRow.Attributes)
	if err != nil {
		p.logger.Warn("bucket key evaluation failed", "line", csvRow.LineNumber, "error", err)
		p.cp.IncrementFailure(1)
		p.result.ValidationErrors++
		return "", false
	}
	return bucketKey, true
}

// createAndInsertPosition creates a domain.Position and adds it to the batch inserter.
// Returns an error only for fatal errors that should stop processing.
func (p *importProcessor) createAndInsertPosition(ctx context.Context, csvRow *csvadapter.ImportRow, bucketKey string) error {
	amount, err := decimal.NewFromString(csvRow.Amount)
	if err != nil {
		p.recordValidationFailure()
		return nil //nolint:nilerr // Validation failures are non-fatal
	}

	position, err := domain.NewPosition(
		csvRow.AccountID,
		csvRow.InstrumentCode,
		bucketKey,
		amount,
		"", // Dimension set from instrument definition
		csvRow.Attributes,
		uuid.New(),
		p.cfg.TenantID,
	)
	if err != nil {
		p.recordValidationFailure()
		return nil //nolint:nilerr // Validation failures are non-fatal
	}

	if err := p.batchInserter.Add(ctx, position); err != nil {
		return fmt.Errorf("failed to add position to batch: %w", err)
	}

	p.cp.IncrementSuccess(1)
	p.result.ImportedRows++
	p.cp.AddRollbackStatement(fmt.Sprintf("DELETE FROM positions WHERE id = '%s'", position.ID))
	return nil
}

// recordValidationFailure increments the failure counters.
func (p *importProcessor) recordValidationFailure() {
	p.cp.IncrementFailure(1)
	p.result.ValidationErrors++
}

// processBatch handles a batch of CSV rows during import.
// Returns context.Canceled if the context is cancelled, nil otherwise.
func (p *importProcessor) processBatch(ctx context.Context, batch csvadapter.RowBatch, checkpointMgr *checkpoint.PostgresManager) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	for i := range batch.Rows {
		if rowErr := p.processRow(ctx, &batch.Rows[i]); rowErr != nil {
			return rowErr
		}
	}
	for range batch.Errors {
		p.cp.IncrementFailure(1)
		p.result.ValidationErrors++
	}
	if updateErr := checkpointMgr.UpdateProgress(ctx, p.cp); updateErr != nil {
		p.logger.Warn("failed to update checkpoint", "error", updateErr)
	}
	return nil
}

// importDependencies holds initialized dependencies for live import.
type importDependencies struct {
	file              *os.File
	instrumentChecker *validation.InstrumentChecker
	csvParser         *csvadapter.Parser
	celEval           *infra.CELEvaluator
	pipeline          *validation.Pipeline
	batchInserter     *infra.BatchInserter
}

// close releases all resources held by import dependencies.
func (d *importDependencies) close() {
	if d.file != nil {
		_ = d.file.Close()
	}
	if d.instrumentChecker != nil {
		_ = d.instrumentChecker.Close()
	}
	if d.pipeline != nil {
		_ = d.pipeline.Close()
	}
}

// initImportDependencies creates all dependencies needed for live import.
func initImportDependencies(ctx context.Context, cfg *importConfig, pool *pgxpool.Pool, logger *slog.Logger) (*importDependencies, error) {
	deps := &importDependencies{}

	var err error
	deps.file, err = os.Open(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to open source file: %w", err)
	}

	deps.instrumentChecker, err = validation.NewInstrumentChecker(ctx, validation.InstrumentCheckerConfig{
		Target:                   getEnvOrDefault("REFERENCE_DATA_URL", "localhost:50051"),
		CreateMissingInstruments: cfg.CreateInstruments,
		Logger:                   logger,
	})
	if err != nil {
		deps.close()
		return nil, fmt.Errorf("failed to create instrument checker: %w", err)
	}

	deps.csvParser = csvadapter.NewParser(&instrumentCheckerRegistry{checker: deps.instrumentChecker})

	deps.celEval, err = infra.NewCELEvaluatorDefault()
	if err != nil {
		deps.close()
		return nil, fmt.Errorf("failed to create CEL evaluator: %w", err)
	}

	deps.pipeline, err = validation.NewPipeline(validation.PipelineConfig{
		DuplicateChecker:         validation.NewDuplicateChecker(validation.DefaultBloomFilterConfig(), createDuplicateLookup(pool)),
		InstrumentChecker:        deps.instrumentChecker,
		CreateMissingInstruments: cfg.CreateInstruments,
		Logger:                   logger,
	})
	if err != nil {
		deps.close()
		return nil, fmt.Errorf("failed to create validation pipeline: %w", err)
	}

	deps.batchInserter, err = infra.NewBatchInserter(infra.BatchInserterConfig{
		Pool:      pool,
		BatchSize: cfg.BatchSize,
		OnBatchComplete: func(batchNum, positionsInBatch, totalInserted int) {
			logger.Debug("batch inserted", "batch", batchNum, "count", positionsInBatch, "total", totalInserted)
		},
	})
	if err != nil {
		deps.close()
		return nil, fmt.Errorf("failed to create batch inserter: %w", err)
	}

	return deps, nil
}

// handleImportInterrupt handles cleanup when import is interrupted.
func handleImportInterrupt(_ context.Context, deps *importDependencies, cp *checkpoint.Checkpoint, checkpointMgr *checkpoint.PostgresManager, result *importResult, logger *slog.Logger) *importResult {
	// Use fresh context for cleanup since the original may be canceled.
	// This is intentional - we need cleanup to succeed even when the parent context is done.
	cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if flushErr := deps.batchInserter.Flush(cleanupCtx); flushErr != nil { //nolint:contextcheck // uses cleanup context created above
		logger.Warn("failed to flush batch on interrupt", "error", flushErr)
	}
	if cancelErr := checkpointMgr.Cancel(cleanupCtx, cp); cancelErr != nil { //nolint:contextcheck // uses cleanup context created above
		logger.Warn("failed to save checkpoint on interrupt", "error", cancelErr)
	}
	result.Interrupted = true
	result.CheckpointRow = int64(cp.ProcessedRows)
	result.TotalRows = int64(cp.TotalRows)
	logger.Info("import interrupted, checkpoint saved", "checkpoint_row", cp.ProcessedRows, "manifest_id", cp.ManifestID)
	return result
}

// finalizeImport flushes remaining positions and marks checkpoint complete.
func finalizeImport(ctx context.Context, deps *importDependencies, cp *checkpoint.Checkpoint, checkpointMgr *checkpoint.PostgresManager, result *importResult, parseResult *csvadapter.ParseResult, logger *slog.Logger) (*importResult, error) {
	if flushErr := deps.batchInserter.Flush(ctx); flushErr != nil {
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
	logger.Info("import complete", "total_rows", result.TotalRows, "imported", result.ImportedRows, "errors", result.ValidationErrors, "manifest_id", result.ManifestID)
	return result, nil
}

// executeLiveImport performs the actual import with checkpoint persistence.
func executeLiveImport(ctx context.Context, cfg *importConfig, cp *checkpoint.Checkpoint, checkpointMgr *checkpoint.PostgresManager, pool *pgxpool.Pool, logger *slog.Logger) (*importResult, error) {
	result := &importResult{ManifestID: cp.ManifestID}

	deps, err := initImportDependencies(ctx, cfg, pool, logger)
	if err != nil {
		return nil, err
	}
	defer deps.close()

	proc := &importProcessor{
		cfg: cfg, cp: cp, result: result,
		instrumentChecker: deps.instrumentChecker, celEval: deps.celEval,
		pipeline: deps.pipeline, batchInserter: deps.batchInserter, logger: logger,
		fungibilityExprs: make(map[string]string),
	}

	parseResult, parseErr := deps.csvParser.Parse(ctx, deps.file, csvadapter.ParseConfig{BatchSize: cfg.BatchSize, SkipEmptyRows: true}, func(batch csvadapter.RowBatch) error {
		return proc.processBatch(ctx, batch, checkpointMgr)
	})

	return handleParseResult(ctx, deps, cp, checkpointMgr, result, parseResult, parseErr, logger)
}

// handleParseResult handles the outcome of CSV parsing and returns the final result.
func handleParseResult(ctx context.Context, deps *importDependencies, cp *checkpoint.Checkpoint, checkpointMgr *checkpoint.PostgresManager, result *importResult, parseResult *csvadapter.ParseResult, parseErr error, logger *slog.Logger) (*importResult, error) {
	if errors.Is(parseErr, context.Canceled) {
		return handleImportInterrupt(ctx, deps, cp, checkpointMgr, result, logger), nil
	}
	if parseErr != nil {
		if failErr := checkpointMgr.Fail(ctx, cp, parseErr); failErr != nil {
			logger.Warn("failed to mark checkpoint as failed", "error", failErr)
		}
		return nil, fmt.Errorf("import failed: %w", parseErr)
	}
	return finalizeImport(ctx, deps, cp, checkpointMgr, result, parseResult, logger)
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
