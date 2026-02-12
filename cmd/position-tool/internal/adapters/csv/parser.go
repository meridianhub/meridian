package csvadapter

import (
	"bufio"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/meridianhub/meridian/shared/platform/quantity"
)

// Mandatory columns required in every import CSV file.
// These are derived from the InstrumentAmount proto message.
const (
	ColumnAccountID      = "account_id"
	ColumnInstrumentCode = "instrument_code"
	ColumnAmount         = "amount"
	ColumnTimestamp      = "timestamp"
)

// MandatoryColumns lists all required columns for CSV import.
var MandatoryColumns = []string{
	ColumnAccountID,
	ColumnInstrumentCode,
	ColumnAmount,
	ColumnTimestamp,
}

// Error types for CSV parsing.
var (
	// ErrEmptyFile is returned when the CSV file has no data rows.
	ErrEmptyFile = errors.New("CSV file is empty or has no data rows")

	// ErrMissingHeader is returned when a mandatory column is missing.
	ErrMissingHeader = errors.New("missing mandatory column")

	// ErrMissingAttributeColumn is returned when an attribute column referenced in the
	// instrument's CEL expression is missing from the CSV.
	ErrMissingAttributeColumn = errors.New("missing attribute column referenced in fungibility key expression")

	// ErrInstrumentNotFound is returned when the instrument code cannot be resolved.
	ErrInstrumentNotFound = errors.New("instrument not found")

	// ErrParseRow is returned when a row cannot be parsed.
	ErrParseRow = errors.New("row parse error")

	// ErrInvalidTimestamp is returned when a timestamp cannot be parsed.
	ErrInvalidTimestamp = errors.New("invalid timestamp format")

	// ErrMixedInstruments is returned when a CSV contains multiple instrument codes.
	ErrMixedInstruments = errors.New("CSV contains multiple instrument codes; only single-instrument files are supported")

	// ErrStrictMode is returned when strict mode is enabled and extra columns are found.
	ErrStrictMode = errors.New("strict mode violation")

	// ErrRequiredField is returned when a required field is empty.
	ErrRequiredField = errors.New("required field is empty")

	// ErrTimestampParse is returned when a timestamp cannot be parsed.
	ErrTimestampParse = errors.New("could not parse timestamp")
)

// ImportRow represents a single parsed row ready for validation and import.
type ImportRow struct {
	// LineNumber is the 1-indexed line number in the source CSV (including header).
	LineNumber int

	// AccountID is the target account for this position.
	AccountID string

	// InstrumentCode is the instrument identifier (e.g., "KWH", "USD").
	InstrumentCode string

	// Amount is the decimal amount as a string for arbitrary precision.
	Amount string

	// Timestamp is the parsed timestamp for this measurement.
	Timestamp time.Time

	// Attributes contains the key-value attributes extracted from CSV columns.
	// Keys are normalized to snake_case.
	Attributes map[string]string

	// RawRow contains the original CSV values for debugging.
	RawRow []string
}

// SchemaMapping defines how CSV columns map to import fields.
type SchemaMapping struct {
	// InstrumentCode is the instrument this mapping applies to.
	InstrumentCode string

	// InstrumentVersion is the version of the instrument definition.
	InstrumentVersion int32

	// FungibilityKeyExpression is the CEL expression for bucket key generation.
	FungibilityKeyExpression string

	// HeaderIndexes maps column names to their 0-indexed position.
	HeaderIndexes map[string]int

	// AttributeKeys contains the attribute keys extracted from the CEL expression.
	AttributeKeys []string

	// ExtraColumns contains column names that are not mandatory or CEL-referenced.
	ExtraColumns []string
}

// Parser provides streaming CSV parsing with schema-driven column mapping.
type Parser struct {
	registry quantity.InstrumentRegistry
}

// NewParser creates a new CSV parser with the given instrument registry.
func NewParser(registry quantity.InstrumentRegistry) *Parser {
	return &Parser{
		registry: registry,
	}
}

// ParseConfig configures the parsing behavior.
type ParseConfig struct {
	// BatchSize is the number of rows to emit per batch (default: 1000).
	BatchSize int

	// SkipEmptyRows skips rows where all values are empty (default: true).
	SkipEmptyRows bool

	// StrictHeaders fails if extra columns are present (default: false).
	// When false, extra columns generate a warning but parsing continues.
	StrictHeaders bool

	// TimestampFormats specifies the acceptable timestamp formats to try.
	// Defaults to RFC3339 and common variants if not specified.
	TimestampFormats []string
}

// DefaultParseConfig returns a ParseConfig with sensible defaults.
func DefaultParseConfig() ParseConfig {
	return ParseConfig{
		BatchSize:     1000,
		SkipEmptyRows: true,
		StrictHeaders: false,
		TimestampFormats: []string{
			time.RFC3339,
			time.RFC3339Nano,
			"2006-01-02T15:04:05Z",
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
			"2006-01-02",
		},
	}
}

// ParseResult contains the results of parsing.
type ParseResult struct {
	// Schema is the discovered schema mapping.
	Schema *SchemaMapping

	// RowCount is the total number of data rows parsed.
	RowCount int

	// ErrorCount is the number of rows that failed to parse.
	ErrorCount int

	// Warnings contains non-fatal warnings (e.g., extra columns).
	Warnings []string
}

// RowBatch contains a batch of parsed rows.
type RowBatch struct {
	// Rows contains the successfully parsed rows in this batch.
	Rows []ImportRow

	// Errors contains row-level parse errors.
	Errors []RowError
}

// RowError represents an error parsing a specific row.
type RowError struct {
	// LineNumber is the 1-indexed line number.
	LineNumber int

	// Column is the column name that caused the error (if applicable).
	Column string

	// Value is the problematic value.
	Value string

	// Err is the underlying error.
	Err error
}

func (e RowError) Error() string {
	if e.Column != "" {
		return fmt.Sprintf("line %d, column %q: %v (value: %q)", e.LineNumber, e.Column, e.Err, e.Value)
	}
	return fmt.Sprintf("line %d: %v", e.LineNumber, e.Err)
}

// Parse reads a CSV file and streams batches of parsed rows.
// It discovers the schema from the first data row and validates subsequent rows.
//
// The parsing flow:
//  1. Read and validate headers against mandatory columns
//  2. Read first data row to discover instrument_code
//  3. Look up instrument definition to get fungibility_key_expression
//  4. Extract required attribute keys from CEL expression
//  5. Validate that all required attribute columns exist
//  6. Stream remaining rows as batches
func (p *Parser) Parse(ctx context.Context, reader io.Reader, config ParseConfig, batchHandler func(RowBatch) error) (*ParseResult, error) {
	config = p.normalizeConfig(config)

	csvReader := p.createCSVReader(reader)
	result := &ParseResult{}

	// Step 1-5: Initialize schema from headers and first row
	schema, headers, firstRow, err := p.initializeSchema(ctx, csvReader)
	if err != nil {
		return nil, err
	}

	// Check for extra columns (warning only unless strict)
	if err := p.checkExtraColumns(headers, schema, config, result); err != nil {
		return nil, err
	}
	result.Schema = schema

	// Step 6: Process rows and stream batches
	return p.processRows(ctx, csvReader, schema, firstRow, config, result, batchHandler)
}

// normalizeConfig ensures config has valid defaults.
func (p *Parser) normalizeConfig(config ParseConfig) ParseConfig {
	if config.BatchSize <= 0 {
		config.BatchSize = DefaultParseConfig().BatchSize
	}
	if len(config.TimestampFormats) == 0 {
		config.TimestampFormats = DefaultParseConfig().TimestampFormats
	}
	return config
}

// createCSVReader creates a configured CSV reader.
func (p *Parser) createCSVReader(reader io.Reader) *csv.Reader {
	bufReader := bufio.NewReader(reader)
	csvReader := csv.NewReader(bufReader)
	csvReader.FieldsPerRecord = -1 // Allow variable field counts
	csvReader.TrimLeadingSpace = true
	csvReader.ReuseRecord = false // We store records, so don't reuse
	return csvReader
}

// initializeSchema reads headers and first row to build the schema mapping.
func (p *Parser) initializeSchema(ctx context.Context, csvReader *csv.Reader) (*SchemaMapping, []string, []string, error) {
	// Read and validate headers
	headers, err := csvReader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, nil, ErrEmptyFile
		}
		return nil, nil, nil, fmt.Errorf("reading CSV headers: %w", err)
	}

	headerIndexes, err := p.validateAndMapHeaders(headers)
	if err != nil {
		return nil, nil, nil, err
	}

	// Read first data row to get instrument_code
	firstRow, err := csvReader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, nil, ErrEmptyFile
		}
		return nil, nil, nil, fmt.Errorf("reading first data row: %w", err)
	}

	instrumentCode := p.extractInstrumentCode(headerIndexes, firstRow)
	if instrumentCode == "" {
		return nil, nil, nil, fmt.Errorf("%w: instrument_code is empty in first row", ErrParseRow)
	}

	// Look up instrument definition
	instrumentDef, err := p.registry.GetActiveDefinition(ctx, instrumentCode)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%w: %s: %w", ErrInstrumentNotFound, instrumentCode, err)
	}

	// Build schema
	attributeKeys := ExtractAttributeKeys(instrumentDef.FungibilityKeyExpression)
	schema := &SchemaMapping{
		InstrumentCode:           instrumentCode,
		InstrumentVersion:        instrumentDef.Version,
		FungibilityKeyExpression: instrumentDef.FungibilityKeyExpression,
		HeaderIndexes:            headerIndexes,
		AttributeKeys:            attributeKeys,
	}

	// Validate attribute columns
	missingAttrs := p.validateAttributeColumns(headerIndexes, attributeKeys)
	if len(missingAttrs) > 0 {
		return nil, nil, nil, fmt.Errorf("%w: %v (expression: %s)",
			ErrMissingAttributeColumn, missingAttrs, instrumentDef.FungibilityKeyExpression)
	}

	return schema, headers, firstRow, nil
}

// extractInstrumentCode gets the instrument code from the first row.
func (p *Parser) extractInstrumentCode(headerIndexes map[string]int, row []string) string {
	if idx, ok := headerIndexes[ColumnInstrumentCode]; ok && idx < len(row) {
		return strings.TrimSpace(row[idx])
	}
	return ""
}

// checkExtraColumns validates extra columns and adds warnings.
func (p *Parser) checkExtraColumns(headers []string, schema *SchemaMapping, config ParseConfig, result *ParseResult) error {
	extraCols := p.findExtraColumns(headers, schema.AttributeKeys)
	schema.ExtraColumns = extraCols
	if len(extraCols) > 0 {
		warning := fmt.Sprintf("extra columns not referenced in schema: %v", extraCols)
		result.Warnings = append(result.Warnings, warning)
		if config.StrictHeaders {
			return fmt.Errorf("%w: %s", ErrStrictMode, warning)
		}
	}
	return nil
}

// processRows streams through CSV rows and emits batches.
func (p *Parser) processRows(ctx context.Context, csvReader *csv.Reader, schema *SchemaMapping, firstRow []string, config ParseConfig, result *ParseResult, batchHandler func(RowBatch) error) (*ParseResult, error) {
	batch := newRowBatch(config.BatchSize)

	// Process first row (header is line 1, so first data row is line 2)
	lineNumber := 2
	p.addRowToBatch(lineNumber, firstRow, schema, config.TimestampFormats, &batch, result)

	// Process remaining rows
	_, err := p.streamRemainingRows(ctx, csvReader, schema, config, lineNumber, &batch, result, batchHandler)
	if err != nil {
		return result, err
	}

	// Emit final batch
	if err := p.emitBatchIfNotEmpty(batch, batchHandler); err != nil {
		return result, err
	}

	return result, nil
}

// newRowBatch creates a new empty batch with pre-allocated capacity.
func newRowBatch(capacity int) RowBatch {
	return RowBatch{
		Rows:   make([]ImportRow, 0, capacity),
		Errors: make([]RowError, 0),
	}
}

// streamRemainingRows processes all rows after the first and emits full batches.
func (p *Parser) streamRemainingRows(ctx context.Context, csvReader *csv.Reader, schema *SchemaMapping, config ParseConfig, startLine int, batch *RowBatch, result *ParseResult, batchHandler func(RowBatch) error) (int, error) {
	lineNumber := startLine

	for {
		if err := ctx.Err(); err != nil {
			return lineNumber, err
		}

		record, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		// Increment line number after successful read to ensure accurate error reporting
		lineNumber++
		if err != nil {
			return lineNumber, fmt.Errorf("reading CSV at line %d: %w", lineNumber, err)
		}

		if config.SkipEmptyRows && isEmptyRow(record) {
			continue
		}

		if err := p.processRecord(lineNumber, record, schema, config, batch, result); err != nil {
			return lineNumber, err
		}

		if err := p.emitBatchIfFull(batch, config.BatchSize, batchHandler); err != nil {
			return lineNumber, err
		}
	}

	return lineNumber, nil
}

// emitBatchIfFull emits the batch and resets it if it has reached capacity.
func (p *Parser) emitBatchIfFull(batch *RowBatch, capacity int, batchHandler func(RowBatch) error) error {
	if len(batch.Rows) >= capacity {
		if err := batchHandler(*batch); err != nil {
			return fmt.Errorf("batch handler error: %w", err)
		}
		*batch = newRowBatch(capacity)
	}
	return nil
}

// emitBatchIfNotEmpty emits the batch if it contains any rows or errors.
func (p *Parser) emitBatchIfNotEmpty(batch RowBatch, batchHandler func(RowBatch) error) error {
	if len(batch.Rows) > 0 || len(batch.Errors) > 0 {
		if err := batchHandler(batch); err != nil {
			return fmt.Errorf("batch handler error: %w", err)
		}
	}
	return nil
}

// addRowToBatch parses a row and adds it to the batch.
func (p *Parser) addRowToBatch(lineNumber int, record []string, schema *SchemaMapping, formats []string, batch *RowBatch, result *ParseResult) {
	row, rowErr := p.parseRow(lineNumber, record, schema, formats)
	if rowErr != nil {
		batch.Errors = append(batch.Errors, *rowErr)
		result.ErrorCount++
	} else if row != nil {
		batch.Rows = append(batch.Rows, *row)
		result.RowCount++
	}
}

// processRecord handles a single CSV record.
// Returns an error only for fatal conditions (e.g., mixed instruments).
// Row-level parse errors are accumulated in batch.Errors.
func (p *Parser) processRecord(lineNumber int, record []string, schema *SchemaMapping, config ParseConfig, batch *RowBatch, result *ParseResult) error {
	row, rowErr := p.parseRow(lineNumber, record, schema, config.TimestampFormats)
	if rowErr != nil {
		batch.Errors = append(batch.Errors, *rowErr)
		result.ErrorCount++
		return nil //nolint:nilerr // Row errors are accumulated, not returned
	}

	if row == nil {
		return nil
	}

	// Verify instrument_code matches (no mixed instruments)
	if row.InstrumentCode != schema.InstrumentCode {
		return fmt.Errorf("%w: expected %q, got %q at line %d",
			ErrMixedInstruments, schema.InstrumentCode, row.InstrumentCode, lineNumber)
	}

	batch.Rows = append(batch.Rows, *row)
	result.RowCount++
	return nil
}

// validateAndMapHeaders checks mandatory columns and builds the header index map.
func (p *Parser) validateAndMapHeaders(headers []string) (map[string]int, error) {
	headerIndexes := make(map[string]int)

	for i, h := range headers {
		normalized := NormalizeHeaderToAttributeKey(h)
		if normalized == "" {
			// Keep original for non-attribute columns
			normalized = strings.TrimSpace(strings.ToLower(h))
		}
		headerIndexes[normalized] = i
	}

	// Check mandatory columns
	var missing []string
	for _, col := range MandatoryColumns {
		if _, ok := headerIndexes[col]; !ok {
			missing = append(missing, col)
		}
	}

	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: %v", ErrMissingHeader, missing)
	}

	return headerIndexes, nil
}

// validateAttributeColumns checks that all CEL-referenced attributes have columns.
func (p *Parser) validateAttributeColumns(headerIndexes map[string]int, attributeKeys []string) []string {
	var missing []string
	for _, key := range attributeKeys {
		if _, ok := headerIndexes[key]; !ok {
			missing = append(missing, key)
		}
	}
	return missing
}

// findExtraColumns identifies columns that are not mandatory or CEL-referenced.
func (p *Parser) findExtraColumns(headers []string, attributeKeys []string) []string {
	mandatorySet := make(map[string]struct{})
	for _, col := range MandatoryColumns {
		mandatorySet[col] = struct{}{}
	}

	attrSet := make(map[string]struct{})
	for _, key := range attributeKeys {
		attrSet[key] = struct{}{}
	}

	extra := make([]string, 0, len(headers))
	for _, h := range headers {
		normalized := NormalizeHeaderToAttributeKey(h)
		if normalized == "" {
			normalized = strings.TrimSpace(strings.ToLower(h))
		}

		if _, isMandatory := mandatorySet[normalized]; isMandatory {
			continue
		}
		if _, isAttr := attrSet[normalized]; isAttr {
			continue
		}
		extra = append(extra, h)
	}

	return extra
}

// parseRow converts a CSV record into an ImportRow.
func (p *Parser) parseRow(lineNumber int, record []string, schema *SchemaMapping, timestampFormats []string) (*ImportRow, *RowError) {
	getField := func(col string) string {
		if idx, ok := schema.HeaderIndexes[col]; ok && idx < len(record) {
			return strings.TrimSpace(record[idx])
		}
		return ""
	}

	// Extract mandatory fields
	accountID := getField(ColumnAccountID)
	if accountID == "" {
		return nil, &RowError{
			LineNumber: lineNumber,
			Column:     ColumnAccountID,
			Err:        fmt.Errorf("%w: %s", ErrRequiredField, ColumnAccountID),
		}
	}

	instrumentCode := getField(ColumnInstrumentCode)
	if instrumentCode == "" {
		return nil, &RowError{
			LineNumber: lineNumber,
			Column:     ColumnInstrumentCode,
			Err:        fmt.Errorf("%w: %s", ErrRequiredField, ColumnInstrumentCode),
		}
	}

	amount := getField(ColumnAmount)
	if amount == "" {
		return nil, &RowError{
			LineNumber: lineNumber,
			Column:     ColumnAmount,
			Err:        fmt.Errorf("%w: %s", ErrRequiredField, ColumnAmount),
		}
	}

	timestampStr := getField(ColumnTimestamp)
	if timestampStr == "" {
		return nil, &RowError{
			LineNumber: lineNumber,
			Column:     ColumnTimestamp,
			Err:        fmt.Errorf("%w: %s", ErrRequiredField, ColumnTimestamp),
		}
	}

	timestamp, err := parseTimestamp(timestampStr, timestampFormats)
	if err != nil {
		return nil, &RowError{
			LineNumber: lineNumber,
			Column:     ColumnTimestamp,
			Value:      timestampStr,
			Err:        fmt.Errorf("%w: %w", ErrInvalidTimestamp, err),
		}
	}

	// Extract attributes
	attributes := make(map[string]string)
	for _, key := range schema.AttributeKeys {
		value := getField(key)
		// Allow empty attribute values - they will be validated downstream
		attributes[key] = value
	}

	return &ImportRow{
		LineNumber:     lineNumber,
		AccountID:      accountID,
		InstrumentCode: instrumentCode,
		Amount:         amount,
		Timestamp:      timestamp,
		Attributes:     attributes,
		RawRow:         record,
	}, nil
}

// parseTimestamp tries multiple timestamp formats.
func parseTimestamp(s string, formats []string) (time.Time, error) {
	for _, format := range formats {
		t, err := time.Parse(format, s)
		if err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: %q", ErrTimestampParse, s)
}

// isEmptyRow returns true if all values in the row are empty.
func isEmptyRow(record []string) bool {
	for _, v := range record {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}
