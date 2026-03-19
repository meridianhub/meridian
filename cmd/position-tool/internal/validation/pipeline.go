package validation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Sentinel errors for pipeline configuration.
var (
	// ErrNilInstrumentChecker indicates that the instrument checker is nil.
	ErrNilInstrumentChecker = errors.New("instrument checker cannot be nil")
)

// PipelineConfig configures the validation pipeline.
type PipelineConfig struct {
	// DuplicateChecker validates measurement ID uniqueness.
	// Required - pipeline will fail if nil.
	DuplicateChecker *DuplicateChecker

	// InstrumentChecker validates instrument existence.
	// Required - pipeline will fail if nil.
	InstrumentChecker InstrumentCheckerInterface

	// SchemaValidator validates attributes against JSON Schema.
	// Optional - schema validation is skipped if nil.
	SchemaValidator *SchemaValidator

	// FieldValidator validates required fields.
	// Optional - uses DefaultRequiredFields if nil.
	FieldValidator *FieldValidator

	// CreateMissingInstruments enables auto-creation of instruments in DRAFT status.
	CreateMissingInstruments bool

	// FailFast stops validation on first error (default: false - collect all errors).
	FailFast bool

	// Logger for structured logging.
	Logger *slog.Logger
}

// Pipeline orchestrates multi-layered validation of import rows.
// It runs each validation layer and collects all errors per row.
//
// Validation layers (in order):
// 1. Required field validation (account_id, instrument_code, amount, bucket_key)
// 2. Duplicate detection (bloom filter + database lookup)
// 3. Instrument existence check (gRPC with caching)
// 4. Attribute schema validation (JSON Schema)
type Pipeline struct {
	duplicateChecker  *DuplicateChecker
	instrumentChecker InstrumentCheckerInterface
	schemaValidator   *SchemaValidator
	fieldValidator    *FieldValidator

	createMissingInstruments bool
	failFast                 bool
	logger                   *slog.Logger

	// Cached instrument schemas keyed by instrument code (protected by schemasMu)
	instrumentSchemas map[string]string
	schemasMu         sync.RWMutex

	// Stats (atomic)
	totalRows          int64
	validRows          int64
	invalidRows        int64
	duplicates         int64
	missingFields      int64
	instrumentErrors   int64
	schemaErrors       int64
	instrumentsCreated int64
}

// NewPipeline creates a new validation pipeline.
func NewPipeline(cfg PipelineConfig) (*Pipeline, error) {
	if cfg.DuplicateChecker == nil {
		return nil, ErrNilDuplicateChecker
	}
	if cfg.InstrumentChecker == nil {
		return nil, ErrNilInstrumentChecker
	}

	fieldValidator := cfg.FieldValidator
	if fieldValidator == nil {
		fieldValidator = NewFieldValidator()
	}

	schemaValidator := cfg.SchemaValidator
	if schemaValidator == nil {
		schemaValidator = NewSchemaValidator()
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Pipeline{
		duplicateChecker:         cfg.DuplicateChecker,
		instrumentChecker:        cfg.InstrumentChecker,
		schemaValidator:          schemaValidator,
		fieldValidator:           fieldValidator,
		createMissingInstruments: cfg.CreateMissingInstruments,
		failFast:                 cfg.FailFast,
		logger:                   logger,
		instrumentSchemas:        make(map[string]string),
	}, nil
}

// ValidateRow validates a single row through all validation layers.
// Returns a RowValidationError containing all errors found.
//
func (p *Pipeline) ValidateRow(ctx context.Context, row *ImportRow) *RowValidationError {
	atomic.AddInt64(&p.totalRows, 1)

	rowErr := &RowValidationError{
		LineNumber: row.LineNumber,
	}

	// Layer 1: Required field validation
	if fieldErrors := p.fieldValidator.Validate(row); len(fieldErrors) > 0 {
		for _, fe := range fieldErrors {
			rowErr.AddError(fe)
			atomic.AddInt64(&p.missingFields, 1)
		}
		if p.failFast && rowErr.HasErrors() {
			atomic.AddInt64(&p.invalidRows, 1)
			return rowErr
		}
	}

	// Layer 2: Duplicate detection
	if row.MeasurementID != "" {
		result, err := p.duplicateChecker.Check(ctx, row.MeasurementID, row.LineNumber)
		if err != nil {
			rowErr.AddError(fmt.Errorf("duplicate check failed: %w", err))
		} else if result.IsDuplicate {
			atomic.AddInt64(&p.duplicates, 1)
			rowErr.AddError(&DuplicateError{
				MeasurementID:      row.MeasurementID,
				ExistingLineNumber: result.ExistingLineNumber,
				InDatabase:         result.InDatabase,
			})
		}

		if p.failFast && rowErr.HasErrors() {
			atomic.AddInt64(&p.invalidRows, 1)
			return rowErr
		}
	}

	// Layer 3: Instrument existence check
	if row.InstrumentCode != "" {
		result, err := p.instrumentChecker.Check(ctx, row.InstrumentCode, row.InstrumentVersion)
		if err != nil {
			rowErr.AddError(fmt.Errorf("instrument check failed: %w", err))
			atomic.AddInt64(&p.instrumentErrors, 1)
		} else if !result.Exists {
			rowErr.AddError(&FieldError{
				Field:  "instrument_code",
				Value:  row.InstrumentCode,
				Reason: "instrument not found in Reference Data Service",
			})
			atomic.AddInt64(&p.instrumentErrors, 1)
		} else if !result.IsActive && !result.WasCreated {
			rowErr.AddError(&FieldError{
				Field:  "instrument_code",
				Value:  row.InstrumentCode,
				Reason: fmt.Sprintf("instrument is not in ACTIVE status (current: %s)", result.Definition.Status.String()),
			})
			atomic.AddInt64(&p.instrumentErrors, 1)
		} else {
			// Instrument is valid - cache schema for attribute validation
			if result.Definition != nil && result.Definition.AttributeSchema != "" {
				p.schemasMu.Lock()
				p.instrumentSchemas[row.InstrumentCode] = result.Definition.AttributeSchema
				p.schemasMu.Unlock()
			}

			// Track auto-created instruments
			if result.WasCreated {
				atomic.AddInt64(&p.instrumentsCreated, 1)
				p.logger.Info("auto-created instrument during validation",
					"line", row.LineNumber,
					"instrument_code", row.InstrumentCode,
				)
			}
		}

		if p.failFast && rowErr.HasErrors() {
			atomic.AddInt64(&p.invalidRows, 1)
			return rowErr
		}
	}

	// Layer 4: Attribute schema validation
	p.schemasMu.RLock()
	schema, ok := p.instrumentSchemas[row.InstrumentCode]
	p.schemasMu.RUnlock()
	if ok && schema != "" {
		if err := p.schemaValidator.Validate(row.Attributes, schema); err != nil {
			rowErr.AddError(fmt.Errorf("%w: %w", ErrInvalidAttributeSchema, err))
			atomic.AddInt64(&p.schemaErrors, 1)
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

// ValidateBatch validates multiple rows and returns errors per row.
// Only rows with errors are included in the result.
func (p *Pipeline) ValidateBatch(ctx context.Context, rows []ImportRow) map[int]*RowValidationError {
	results := make(map[int]*RowValidationError)

	for i := range rows {
		rowErr := p.ValidateRow(ctx, &rows[i])
		if rowErr.HasErrors() {
			results[rows[i].LineNumber] = rowErr
		}
	}

	return results
}

// ValidateWithCallback validates rows and calls the callback for each row.
// Returns the first error if failFast is true and validation fails.
//
func (p *Pipeline) ValidateWithCallback(
	ctx context.Context,
	rows []ImportRow,
	onValid func(*ImportRow) error,
	onInvalid func(*ImportRow, *RowValidationError) error,
) error {
	for i := range rows {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rowErr := p.ValidateRow(ctx, &rows[i])

		if rowErr.HasErrors() {
			if onInvalid != nil {
				if err := onInvalid(&rows[i], rowErr); err != nil {
					return err
				}
			}
			if p.failFast {
				return fmt.Errorf("validation failed at line %d: %w", rows[i].LineNumber, rowErr)
			}
		} else {
			if onValid != nil {
				if err := onValid(&rows[i]); err != nil {
					return err
				}
			}
		}
	}

	return nil
}

// Summary returns a summary of the validation run.
func (p *Pipeline) Summary() *Summary {
	dupeStats := p.duplicateChecker.Stats()
	instStats := p.instrumentChecker.Stats()

	return &Summary{
		TotalRows:                 int(atomic.LoadInt64(&p.totalRows)),
		ValidRows:                 int(atomic.LoadInt64(&p.validRows)),
		InvalidRows:               int(atomic.LoadInt64(&p.invalidRows)),
		DuplicateCount:            int(dupeStats.TruePositives),
		MissingFieldCount:         int(atomic.LoadInt64(&p.missingFields)),
		InstrumentNotFoundCount:   int(atomic.LoadInt64(&p.instrumentErrors)),
		SchemaErrorCount:          int(atomic.LoadInt64(&p.schemaErrors)),
		InstrumentsCreated:        int(atomic.LoadInt64(&p.instrumentsCreated)),
		BloomFilterFalsePositives: int(dupeStats.FalsePositives),
		InstrumentCacheHits:       int(instStats.Hits),
		InstrumentCacheMisses:     int(instStats.Misses),
	}
}

// Reset clears all statistics for a new validation run.
func (p *Pipeline) Reset() {
	atomic.StoreInt64(&p.totalRows, 0)
	atomic.StoreInt64(&p.validRows, 0)
	atomic.StoreInt64(&p.invalidRows, 0)
	atomic.StoreInt64(&p.duplicates, 0)
	atomic.StoreInt64(&p.missingFields, 0)
	atomic.StoreInt64(&p.instrumentErrors, 0)
	atomic.StoreInt64(&p.schemaErrors, 0)
	atomic.StoreInt64(&p.instrumentsCreated, 0)
	p.schemasMu.Lock()
	p.instrumentSchemas = make(map[string]string)
	p.schemasMu.Unlock()
}

// Close releases resources used by the pipeline.
func (p *Pipeline) Close() error {
	if p.instrumentChecker != nil {
		return p.instrumentChecker.Close()
	}
	return nil
}

// StreamingValidator provides a streaming interface for validation.
// It processes rows one at a time without buffering the entire dataset.
type StreamingValidator struct {
	pipeline *Pipeline
	start    time.Time
}

// NewStreamingValidator creates a streaming validator backed by a Pipeline.
func NewStreamingValidator(cfg PipelineConfig) (*StreamingValidator, error) {
	pipeline, err := NewPipeline(cfg)
	if err != nil {
		return nil, err
	}

	return &StreamingValidator{
		pipeline: pipeline,
		start:    time.Now(),
	}, nil
}

// Validate validates a single row and returns any errors.
func (sv *StreamingValidator) Validate(ctx context.Context, row *ImportRow) *RowValidationError {
	return sv.pipeline.ValidateRow(ctx, row)
}

// Summary returns the validation summary with duration.
func (sv *StreamingValidator) Summary() *Summary {
	summary := sv.pipeline.Summary()
	summary.Duration = time.Since(sv.start)
	return summary
}

// Close releases resources.
func (sv *StreamingValidator) Close() error {
	return sv.pipeline.Close()
}
