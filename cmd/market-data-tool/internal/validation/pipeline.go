package validation

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"

	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/infra"
)

// PipelineConfig configures the validation pipeline.
type PipelineConfig struct {
	// DatasetChecker validates dataset existence via gRPC.
	DatasetChecker *DatasetChecker

	// SchemaValidator validates attributes against JSON Schema.
	// Optional - schema validation is skipped if nil.
	SchemaValidator *SchemaValidator

	// CELPreview provides non-authoritative CEL validation preview.
	// Optional - CEL preview is skipped if nil.
	CELPreview *infra.CELPreview

	// FailFast stops validation on first error (default: false - collect all errors).
	FailFast bool

	// Logger for structured logging.
	Logger *slog.Logger
}

// Pipeline orchestrates multi-layered validation of observation rows.
//
// Validation layers (in order):
//  1. Required field validation (value, quality_level)
//  2. Dataset existence check (gRPC with caching)
//  3. Attribute schema validation (JSON Schema)
//  4. CEL validation preview (non-authoritative)
type Pipeline struct {
	datasetChecker  *DatasetChecker
	schemaValidator *SchemaValidator
	celPreview      *infra.CELPreview

	failFast bool
	logger   *slog.Logger

	// Stats (atomic)
	totalRows     int64
	validRows     int64
	invalidRows   int64
	missingFields int64
	datasetErrors int64
	schemaErrors  int64
	celWarnings   int64
}

// NewPipeline creates a new validation pipeline.
func NewPipeline(cfg PipelineConfig) *Pipeline {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Pipeline{
		datasetChecker:  cfg.DatasetChecker,
		schemaValidator: cfg.SchemaValidator,
		celPreview:      cfg.CELPreview,
		failFast:        cfg.FailFast,
		logger:          logger,
	}
}

// ValidateRow validates a single row through all validation layers.
func (p *Pipeline) ValidateRow(ctx context.Context, row *ObservationRow) *RowValidationError {
	atomic.AddInt64(&p.totalRows, 1)

	rowErr := &RowValidationError{
		LineNumber: row.LineNumber,
	}

	// Layer 1: Required field validation
	if err := p.validateRequiredFields(row); err != nil {
		rowErr.AddError(err)
		atomic.AddInt64(&p.missingFields, 1)
		if p.failFast {
			atomic.AddInt64(&p.invalidRows, 1)
			return rowErr
		}
	}

	// Layer 2: Dataset existence check
	if p.datasetChecker != nil {
		if err := p.datasetChecker.Check(ctx, row.DatasetCode); err != nil {
			rowErr.AddError(err)
			atomic.AddInt64(&p.datasetErrors, 1)
			if p.failFast {
				atomic.AddInt64(&p.invalidRows, 1)
				return rowErr
			}
		}
	}

	// Layer 3: Attribute schema validation
	if p.schemaValidator != nil && len(row.Attributes) > 0 {
		if err := p.schemaValidator.Validate(row.Attributes); err != nil {
			rowErr.AddError(fmt.Errorf("%w: %w", ErrInvalidAttributeSchema, err))
			atomic.AddInt64(&p.schemaErrors, 1)
			if p.failFast {
				atomic.AddInt64(&p.invalidRows, 1)
				return rowErr
			}
		}
	}

	// Layer 4: CEL validation preview (non-authoritative)
	if p.celPreview != nil && p.celPreview.IsEnabled() {
		result := p.celPreview.Evaluate(row.Value, row.DatasetCode, row.QualityLevel, row.Attributes)
		if !result.Valid {
			// This is a preview warning, not a hard error
			atomic.AddInt64(&p.celWarnings, 1)
			p.logger.Debug("CEL validation preview failed",
				"line", row.LineNumber,
				"warning", result.Warning,
			)
		}
	}

	// Update stats
	if rowErr.HasErrors() {
		atomic.AddInt64(&p.invalidRows, 1)
	} else {
		atomic.AddInt64(&p.validRows, 1)
	}

	return rowErr
}

// validateRequiredFields checks that all required fields are present.
func (p *Pipeline) validateRequiredFields(row *ObservationRow) error {
	if row.Value == "" {
		return &FieldError{
			Field:  "value",
			Reason: "required field is empty",
		}
	}

	if row.QualityLevel == "" {
		return &FieldError{
			Field:  "quality_level",
			Reason: "required field is empty",
		}
	}

	if row.ObservedAt.IsZero() {
		return &FieldError{
			Field:  "observed_at",
			Reason: "required field is empty or invalid",
		}
	}

	return nil
}

// ValidateBatch validates multiple rows and returns errors per row.
func (p *Pipeline) ValidateBatch(ctx context.Context, rows []ObservationRow) map[int]*RowValidationError {
	results := make(map[int]*RowValidationError)

	for i := range rows {
		rowErr := p.ValidateRow(ctx, &rows[i])
		if rowErr.HasErrors() {
			results[rows[i].LineNumber] = rowErr
		}
	}

	return results
}

// Summary returns a summary of the validation run.
func (p *Pipeline) Summary() *Summary {
	return &Summary{
		TotalRows:         int(atomic.LoadInt64(&p.totalRows)),
		ValidRows:         int(atomic.LoadInt64(&p.validRows)),
		InvalidRows:       int(atomic.LoadInt64(&p.invalidRows)),
		MissingFieldCount: int(atomic.LoadInt64(&p.missingFields)),
		DatasetErrorCount: int(atomic.LoadInt64(&p.datasetErrors)),
		SchemaErrorCount:  int(atomic.LoadInt64(&p.schemaErrors)),
		CELWarningCount:   int(atomic.LoadInt64(&p.celWarnings)),
	}
}

// Reset clears all statistics for a new validation run.
func (p *Pipeline) Reset() {
	atomic.StoreInt64(&p.totalRows, 0)
	atomic.StoreInt64(&p.validRows, 0)
	atomic.StoreInt64(&p.invalidRows, 0)
	atomic.StoreInt64(&p.missingFields, 0)
	atomic.StoreInt64(&p.datasetErrors, 0)
	atomic.StoreInt64(&p.schemaErrors, 0)
	atomic.StoreInt64(&p.celWarnings, 0)
}
