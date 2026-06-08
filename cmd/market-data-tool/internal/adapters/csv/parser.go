// Package csvadapter provides CSV parsing for market data observation imports.
//
// The parser handles:
//   - Required columns: observed_at, quality_level, value
//   - Optional temporal bounds: valid_from, valid_to
//   - Dynamic attribute extraction based on dataset attribute_schema
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

	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/infra"
)

// Required columns for observation CSV files.
const (
	ColumnObservedAt   = "observed_at"
	ColumnQualityLevel = "quality_level"
	ColumnValue        = "value"
)

// Optional columns for observation CSV files.
const (
	ColumnValidFrom = "valid_from"
	ColumnValidTo   = "valid_to"
)

// MandatoryColumns lists all required columns for CSV import.
var MandatoryColumns = []string{
	ColumnObservedAt,
	ColumnQualityLevel,
	ColumnValue,
}

// OptionalColumns lists optional columns that have special handling.
var OptionalColumns = []string{
	ColumnValidFrom,
	ColumnValidTo,
}

// validQualityLevels maps accepted quality-level strings to their canonical
// uppercase form. The four canonical confidence grades on Axis A of the two-axis
// quality model (ADR-0017) are ESTIMATE, PROVISIONAL, ACTUAL, and VERIFIED.
// REVISED is accepted as a legacy label; downstream parsing (ParseQualityString)
// normalizes it to VERIFIED confidence with revision 1.
var validQualityLevels = map[string]string{
	"ESTIMATE":    "ESTIMATE",
	"PROVISIONAL": "PROVISIONAL",
	"ACTUAL":      "ACTUAL",
	"VERIFIED":    "VERIFIED",
	"REVISED":     "REVISED", // legacy label, normalizes to VERIFIED + revision 1
	// Also accept lowercase and title case
	"estimate":    "ESTIMATE",
	"provisional": "PROVISIONAL",
	"actual":      "ACTUAL",
	"verified":    "VERIFIED",
	"revised":     "REVISED",
	"Estimate":    "ESTIMATE",
	"Provisional": "PROVISIONAL",
	"Actual":      "ACTUAL",
	"Verified":    "VERIFIED",
	"Revised":     "REVISED",
}

// Error types for CSV parsing.
var (
	// ErrEmptyFile is returned when the CSV file has no data rows.
	ErrEmptyFile = errors.New("CSV file is empty or has no data rows")

	// ErrMissingHeader is returned when a mandatory column is missing.
	ErrMissingHeader = errors.New("missing mandatory column")

	// ErrParseRow is returned when a row cannot be parsed.
	ErrParseRow = errors.New("row parse error")

	// ErrInvalidTimestamp is returned when a timestamp cannot be parsed.
	ErrInvalidTimestamp = errors.New("invalid timestamp format")

	// ErrInvalidQualityLevel is returned when an invalid quality level is specified.
	ErrInvalidQualityLevel = errors.New("invalid quality level")

	// ErrRequiredField is returned when a required field is empty.
	ErrRequiredField = errors.New("required field is empty")

	// ErrValueTooLong is returned when the value exceeds maximum length.
	ErrValueTooLong = errors.New("value exceeds maximum length of 64 characters")

	// ErrTimestampParse is returned when a timestamp cannot be parsed.
	ErrTimestampParse = errors.New("could not parse timestamp")
)

// ObservationRow represents a single parsed observation row ready for import.
type ObservationRow struct {
	// LineNumber is the 1-indexed line number in the source CSV (including header).
	LineNumber int

	// DatasetCode is set during processing (not from CSV).
	DatasetCode string

	// Value is the observed value as a string (max 64 chars).
	Value string

	// ObservedAt is when the observation was made.
	ObservedAt time.Time

	// ValidFrom is when the observation value becomes valid (optional).
	ValidFrom *time.Time

	// ValidTo is when the observation value expires (optional).
	ValidTo *time.Time

	// QualityLevel is the confidence grade on Axis A of the two-axis quality
	// model (ADR-0017): ESTIMATE, PROVISIONAL, ACTUAL, or VERIFIED. The legacy
	// REVISED label is accepted and passed through for downstream normalization.
	QualityLevel string

	// Attributes contains dynamic attributes extracted from CSV columns.
	// Keys are column names from the CSV (matching attribute_schema).
	Attributes map[string]string

	// RawRow contains the original CSV values for debugging.
	RawRow []string
}

// SchemaMapping defines how CSV columns map to import fields.
type SchemaMapping struct {
	// DatasetCode is the dataset this mapping applies to.
	DatasetCode string

	// HeaderIndexes maps column names to their 0-indexed position.
	HeaderIndexes map[string]int

	// AttributeKeys contains the attribute keys to extract from CSV.
	AttributeKeys []string

	// ExtraColumns contains column names that are not mandatory or schema-referenced.
	ExtraColumns []string
}

// Parser provides streaming CSV parsing with schema-driven column mapping.
type Parser struct {
	dataset *infra.DataSetDefinition
}

// NewParser creates a new CSV parser with the given dataset definition.
func NewParser(dataset *infra.DataSetDefinition) *Parser {
	return &Parser{
		dataset: dataset,
	}
}

// ParseConfig configures the parsing behavior.
// Use DefaultParseConfig() to get a config with sensible defaults, then override as needed.
type ParseConfig struct {
	// BatchSize is the number of rows to emit per batch (default: 1000).
	BatchSize int

	// SkipEmptyRows skips rows where all values are empty.
	// NOTE: This field is NOT auto-defaulted because Go's zero value (false) is valid.
	// Use DefaultParseConfig() to get the recommended default (true).
	SkipEmptyRows bool

	// skipEmptyRowsSet tracks whether SkipEmptyRows was explicitly configured.
	// This is set automatically by WithSkipEmptyRows.
	skipEmptyRowsSet bool

	// TimestampFormats specifies the acceptable timestamp formats to try.
	// Defaults to RFC3339 and common variants if not specified.
	TimestampFormats []string
}

// DefaultParseConfig returns a ParseConfig with sensible defaults.
func DefaultParseConfig() ParseConfig {
	return ParseConfig{
		BatchSize:        1000,
		SkipEmptyRows:    true,
		skipEmptyRowsSet: true,
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

// WithSkipEmptyRows returns a copy of the config with SkipEmptyRows explicitly set.
func (c ParseConfig) WithSkipEmptyRows(skip bool) ParseConfig {
	c.SkipEmptyRows = skip
	c.skipEmptyRowsSet = true
	return c
}

// ParseResult contains the results of parsing.
type ParseResult struct {
	// Schema is the discovered schema mapping.
	Schema *SchemaMapping

	// RowCount is the total number of data rows parsed successfully.
	RowCount int

	// ErrorCount is the number of rows that failed to parse.
	ErrorCount int

	// Warnings contains non-fatal warnings (e.g., extra columns).
	Warnings []string
}

// RowBatch contains a batch of parsed rows.
type RowBatch struct {
	// Rows contains the successfully parsed rows in this batch.
	Rows []ObservationRow

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

// ErrNilDataset is returned when the parser's dataset is nil.
var ErrNilDataset = errors.New("dataset definition is required")

// Parse reads a CSV file and streams batches of parsed rows.
func (p *Parser) Parse(ctx context.Context, reader io.Reader, config ParseConfig, batchHandler func(RowBatch) error) (*ParseResult, error) {
	if p.dataset == nil {
		return nil, ErrNilDataset
	}

	config = p.normalizeConfig(config)

	csvReader := p.createCSVReader(reader)
	result := &ParseResult{}

	// Read and validate headers
	schema, headers, err := p.initializeSchema(csvReader)
	if err != nil {
		return nil, err
	}
	result.Schema = schema

	// Check for extra columns (warning only)
	p.checkExtraColumns(headers, schema, result)

	// Process rows and stream batches
	return p.processRows(ctx, csvReader, schema, config, result, batchHandler)
}

// normalizeConfig ensures config has valid defaults.
func (p *Parser) normalizeConfig(config ParseConfig) ParseConfig {
	defaults := DefaultParseConfig()
	if config.BatchSize <= 0 {
		config.BatchSize = defaults.BatchSize
	}
	if len(config.TimestampFormats) == 0 {
		config.TimestampFormats = defaults.TimestampFormats
	}
	// Apply SkipEmptyRows default only if not explicitly set
	if !config.skipEmptyRowsSet {
		config.SkipEmptyRows = defaults.SkipEmptyRows
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

// initializeSchema reads headers and builds the schema mapping.
func (p *Parser) initializeSchema(csvReader *csv.Reader) (*SchemaMapping, []string, error) {
	// Read and validate headers
	headers, err := csvReader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil, ErrEmptyFile
		}
		return nil, nil, fmt.Errorf("reading CSV headers: %w", err)
	}

	headerIndexes, err := p.validateAndMapHeaders(headers)
	if err != nil {
		return nil, nil, err
	}

	// Extract attribute keys from dataset schema
	attributeKeys := p.extractAttributeKeys(headers, headerIndexes)

	schema := &SchemaMapping{
		DatasetCode:   p.dataset.Code,
		HeaderIndexes: headerIndexes,
		AttributeKeys: attributeKeys,
	}

	return schema, headers, nil
}

// validateAndMapHeaders checks mandatory columns and builds the header index map.
func (p *Parser) validateAndMapHeaders(headers []string) (map[string]int, error) {
	headerIndexes := make(map[string]int)

	for i, h := range headers {
		normalized := normalizeColumnName(h)
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

// extractAttributeKeys determines which columns should be extracted as attributes.
func (p *Parser) extractAttributeKeys(headers []string, _ map[string]int) []string {
	// Create sets for quick lookup
	mandatory := make(map[string]bool)
	for _, col := range MandatoryColumns {
		mandatory[col] = true
	}
	optional := make(map[string]bool)
	for _, col := range OptionalColumns {
		optional[col] = true
	}

	// Extract attribute columns (everything not mandatory or optional)
	var attributeKeys []string
	for _, h := range headers {
		normalized := normalizeColumnName(h)
		if !mandatory[normalized] && !optional[normalized] {
			attributeKeys = append(attributeKeys, normalized)
		}
	}

	return attributeKeys
}

// checkExtraColumns identifies and logs extra columns.
func (p *Parser) checkExtraColumns(_ []string, schema *SchemaMapping, result *ParseResult) {
	// All non-mandatory, non-optional columns are considered "extra" for reporting
	// but they're extracted as attributes, so this is informational
	if len(schema.AttributeKeys) > 0 {
		result.Warnings = append(result.Warnings,
			fmt.Sprintf("extracting %d columns as attributes: %v", len(schema.AttributeKeys), schema.AttributeKeys))
	}
}

// processRows streams through CSV rows and emits batches.
func (p *Parser) processRows(ctx context.Context, csvReader *csv.Reader, schema *SchemaMapping, config ParseConfig, result *ParseResult, batchHandler func(RowBatch) error) (*ParseResult, error) {
	batch := newRowBatch(config.BatchSize)
	lineNumber := 1 // Header is line 1

	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		record, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		lineNumber++
		if err != nil {
			return result, fmt.Errorf("reading CSV at line %d: %w", lineNumber, err)
		}

		if config.SkipEmptyRows && isEmptyRow(record) {
			continue
		}

		if rowErr := p.processRecord(lineNumber, record, schema, config, &batch, result); rowErr != nil {
			return result, rowErr
		}

		if err := p.emitBatchIfFull(&batch, config.BatchSize, batchHandler); err != nil {
			return result, err
		}
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
		Rows:   make([]ObservationRow, 0, capacity),
		Errors: make([]RowError, 0),
	}
}

// emitBatchIfFull emits the batch and resets it if it has reached capacity.
func (p *Parser) emitBatchIfFull(batch *RowBatch, capacity int, batchHandler func(RowBatch) error) error {
	if len(batch.Rows)+len(batch.Errors) >= capacity {
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

// processRecord handles a single CSV record.
func (p *Parser) processRecord(lineNumber int, record []string, schema *SchemaMapping, config ParseConfig, batch *RowBatch, result *ParseResult) error {
	row, rowErr := p.parseRow(lineNumber, record, schema, config.TimestampFormats)
	if rowErr != nil {
		batch.Errors = append(batch.Errors, *rowErr)
		result.ErrorCount++
		return nil //nolint:nilerr // Row errors are accumulated, not returned
	}

	if row != nil {
		batch.Rows = append(batch.Rows, *row)
		result.RowCount++
	}

	return nil
}

// parseRow converts a CSV record into an ObservationRow.
func (p *Parser) parseRow(lineNumber int, record []string, schema *SchemaMapping, timestampFormats []string) (*ObservationRow, *RowError) {
	getField := func(col string) string {
		if idx, ok := schema.HeaderIndexes[col]; ok && idx < len(record) {
			return strings.TrimSpace(record[idx])
		}
		return ""
	}

	// Extract and validate required fields
	observedAtStr := getField(ColumnObservedAt)
	if observedAtStr == "" {
		return nil, &RowError{
			LineNumber: lineNumber,
			Column:     ColumnObservedAt,
			Err:        fmt.Errorf("%w: %s", ErrRequiredField, ColumnObservedAt),
		}
	}

	observedAt, err := parseTimestamp(observedAtStr, timestampFormats)
	if err != nil {
		return nil, &RowError{
			LineNumber: lineNumber,
			Column:     ColumnObservedAt,
			Value:      observedAtStr,
			Err:        fmt.Errorf("%w: %w", ErrInvalidTimestamp, err),
		}
	}

	qualityLevelStr := getField(ColumnQualityLevel)
	if qualityLevelStr == "" {
		return nil, &RowError{
			LineNumber: lineNumber,
			Column:     ColumnQualityLevel,
			Err:        fmt.Errorf("%w: %s", ErrRequiredField, ColumnQualityLevel),
		}
	}

	qualityLevel, ok := validQualityLevels[qualityLevelStr]
	if !ok {
		return nil, &RowError{
			LineNumber: lineNumber,
			Column:     ColumnQualityLevel,
			Value:      qualityLevelStr,
			Err:        fmt.Errorf("%w: must be one of ESTIMATE, PROVISIONAL, ACTUAL, VERIFIED (REVISED accepted as legacy)", ErrInvalidQualityLevel),
		}
	}

	value := getField(ColumnValue)
	if value == "" {
		return nil, &RowError{
			LineNumber: lineNumber,
			Column:     ColumnValue,
			Err:        fmt.Errorf("%w: %s", ErrRequiredField, ColumnValue),
		}
	}

	if len(value) > 64 {
		return nil, &RowError{
			LineNumber: lineNumber,
			Column:     ColumnValue,
			Value:      value[:20] + "...",
			Err:        ErrValueTooLong,
		}
	}

	// Extract optional temporal bounds
	var validFrom, validTo *time.Time

	if validFromStr := getField(ColumnValidFrom); validFromStr != "" {
		t, err := parseTimestamp(validFromStr, timestampFormats)
		if err != nil {
			return nil, &RowError{
				LineNumber: lineNumber,
				Column:     ColumnValidFrom,
				Value:      validFromStr,
				Err:        fmt.Errorf("%w: %w", ErrInvalidTimestamp, err),
			}
		}
		validFrom = &t
	}

	if validToStr := getField(ColumnValidTo); validToStr != "" {
		t, err := parseTimestamp(validToStr, timestampFormats)
		if err != nil {
			return nil, &RowError{
				LineNumber: lineNumber,
				Column:     ColumnValidTo,
				Value:      validToStr,
				Err:        fmt.Errorf("%w: %w", ErrInvalidTimestamp, err),
			}
		}
		validTo = &t
	}

	// Extract attributes
	attributes := make(map[string]string)
	for _, key := range schema.AttributeKeys {
		value := getField(key)
		// Include all attributes, even empty ones - let validation handle requirements
		attributes[key] = value
	}

	return &ObservationRow{
		LineNumber:   lineNumber,
		DatasetCode:  schema.DatasetCode,
		Value:        value,
		ObservedAt:   observedAt,
		ValidFrom:    validFrom,
		ValidTo:      validTo,
		QualityLevel: qualityLevel,
		Attributes:   attributes,
		RawRow:       record,
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

// normalizeColumnName converts a column header to a normalized form.
func normalizeColumnName(header string) string {
	// Trim whitespace and convert to lowercase
	normalized := strings.TrimSpace(strings.ToLower(header))
	// Replace common separators with underscore
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	return normalized
}
