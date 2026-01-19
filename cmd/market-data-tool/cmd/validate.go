package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/adapters/csv"
	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/infra"
	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/validation"
)

// Validate command flags.
var (
	validateSource  string
	validateDataset string
)

// Validate command errors.
var (
	// ErrValidationFailed is returned when validation completes with errors.
	ErrValidationFailed = errors.New("validation completed with errors")
)

// validateCmd represents the validate command.
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a CSV file without importing",
	Long: `Validate a CSV file against the dataset schema without importing.

The validate command performs a comprehensive validation preview:

  1. Verifies the dataset exists and is ACTIVE
  2. Validates CSV structure against required columns
  3. Validates attributes against the dataset's attribute_schema
  4. Runs CEL validation expression preview (non-authoritative)

IMPORTANT: CEL validation is a preview only. The authoritative validation
is performed by the Market Information Service during actual import.
Any discrepancies between the preview and service validation will be noted.

Examples:
  # Validate CSV against dataset schema
  market-data-tool validate --tenant=acme_corp --source=rates.csv --dataset=USD_EUR_FX

  # Validate with verbose output
  market-data-tool validate --tenant=acme_corp --source=rates.csv --dataset=USD_EUR_FX --log-level=debug`,
	RunE:          runValidateWrapper,
	SilenceErrors: true,
}

func init() {
	rootCmd.AddCommand(validateCmd)

	validateCmd.Flags().StringVar(&validateSource, "source", "",
		"Path to CSV file to validate (required)")
	validateCmd.Flags().StringVar(&validateDataset, "dataset", "",
		"Dataset code to validate against (required)")

	_ = validateCmd.MarkFlagRequired("source")
	_ = validateCmd.MarkFlagRequired("dataset")
}

// runValidateWrapper handles exit codes for the validate command.
func runValidateWrapper(cmd *cobra.Command, args []string) error {
	err := runValidate(cmd, args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	return nil
}

func runValidate(_ *cobra.Command, _ []string) error {
	// Validate required flags
	if err := validateCommonFlags(); err != nil {
		return err
	}

	if validateDataset == "" {
		return ErrDatasetRequired
	}

	// Validate source file exists
	if _, err := os.Stat(validateSource); os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", ErrSourceNotFound, validateSource)
	}

	// Set up graceful shutdown context
	ctx, cancel := ShutdownContext()
	defer cancel()

	logger := slog.Default()
	logger.Info("starting validation",
		"tenant", tenantID,
		"source", validateSource,
		"dataset", validateDataset,
		"grpc_endpoint", grpcEndpoint,
	)

	start := time.Now()

	// Execute validation
	result, err := executeValidation(ctx, &validateConfig{
		TenantID:     tenantID,
		Source:       validateSource,
		Dataset:      validateDataset,
		GRPCEndpoint: grpcEndpoint,
	})
	if err != nil {
		return err
	}

	elapsed := time.Since(start)

	// Print results
	printValidationResult(result, elapsed)

	// Return error if there were validation failures
	if result.ErrorCount > 0 {
		return fmt.Errorf("%w: %d errors", ErrValidationFailed, result.ErrorCount)
	}

	return nil
}

// validateConfig holds the configuration for a validation operation.
type validateConfig struct {
	TenantID     string
	Source       string
	Dataset      string
	GRPCEndpoint string
}

// validateResult holds the results of a validation operation.
type validateResult struct {
	TotalRows       int64
	ValidRows       int64
	ErrorCount      int64
	CELWarnings     int64
	DatasetStatus   string
	AttributeSchema string
}

// executeValidation performs the validation operation.
func executeValidation(ctx context.Context, cfg *validateConfig) (*validateResult, error) {
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

	// Fetch dataset definition
	dataset, err := grpcClient.GetDataSet(ctx, cfg.Dataset, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dataset definition: %w", err)
	}

	result := &validateResult{
		DatasetStatus:   dataset.Status,
		AttributeSchema: dataset.AttributeSchemaJSON,
	}

	// Open CSV file
	file, err := os.Open(cfg.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to open source file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Create validation pipeline
	celPreview := infra.NewCELPreview(dataset.ValidationExpression)
	pipeline := validation.NewPipeline(validation.PipelineConfig{
		DatasetChecker:  validation.NewDatasetChecker(grpcClient, cfg.Dataset),
		SchemaValidator: validation.NewSchemaValidator(dataset.AttributeSchema),
		CELPreview:      celPreview,
		Logger:          logger,
	})

	// Create CSV parser
	csvParser := csv.NewParser(dataset)

	// Parse and validate CSV
	parseConfig := csv.ParseConfig{
		BatchSize:     500,
		SkipEmptyRows: true,
	}

	parseResult, err := csvParser.Parse(ctx, file, parseConfig, func(batch csv.RowBatch) error {
		for _, csvRow := range batch.Rows {
			validationRow := csvRowToValidationRow(&csvRow, cfg.Dataset)
			rowErr := pipeline.ValidateRow(ctx, validationRow)
			if rowErr != nil && rowErr.HasErrors() {
				result.ErrorCount++
				if logger.Enabled(ctx, slog.LevelDebug) {
					logger.Debug("validation error",
						"line", csvRow.LineNumber,
						"errors", rowErr.Error(),
					)
				}
			} else {
				result.ValidRows++
			}
		}

		for _, rowErr := range batch.Errors {
			result.ErrorCount++
			if logger.Enabled(ctx, slog.LevelDebug) {
				logger.Debug("parse error",
					"line", rowErr.LineNumber,
					"error", rowErr.Err.Error(),
				)
			}
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("CSV parsing failed: %w", err)
	}

	result.TotalRows = int64(parseResult.RowCount) + int64(parseResult.ErrorCount)
	result.ErrorCount += int64(parseResult.ErrorCount)
	result.CELWarnings = int64(celPreview.WarningCount())

	logger.Info("validation complete",
		"total_rows", result.TotalRows,
		"valid_rows", result.ValidRows,
		"errors", result.ErrorCount,
		"cel_warnings", result.CELWarnings,
	)

	return result, nil
}

// printValidationResult outputs the validation results in a formatted report.
func printValidationResult(result *validateResult, elapsed time.Duration) {
	fmt.Println()
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println("|                       VALIDATION SUMMARY                                  |")
	fmt.Println("+---------------------------------------------------------------------------+")
	fmt.Println()

	fmt.Printf("  Dataset Status:    %s\n", result.DatasetStatus)
	fmt.Printf("  Duration:          %s\n", formatDuration(elapsed))
	fmt.Println()

	fmt.Println("  Results:")
	fmt.Printf("    Total Rows:      %d\n", result.TotalRows)
	fmt.Printf("    Valid:           %d\n", result.ValidRows)
	fmt.Printf("    Errors:          %d\n", result.ErrorCount)
	fmt.Println()

	if result.CELWarnings > 0 {
		fmt.Println("  CEL Preview:")
		fmt.Printf("    Warnings:        %d\n", result.CELWarnings)
		fmt.Println("    NOTE: CEL validation is preview-only. Service validation is authoritative.")
		fmt.Println()
	}

	if result.ErrorCount > 0 {
		fmt.Println("  STATUS: VALIDATION FAILED")
	} else {
		fmt.Println("  STATUS: VALIDATION PASSED")
	}

	fmt.Println()
	fmt.Println("+---------------------------------------------------------------------------+")
}
