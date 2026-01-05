package validation

import (
	"time"
)

// ImportRow represents a single row being validated.
// This mirrors csv.ImportRow but is defined here to avoid import cycles.
type ImportRow struct {
	// LineNumber is the 1-indexed line number in the source CSV.
	LineNumber int

	// MeasurementID is the unique identifier for this measurement.
	// Used for duplicate detection.
	MeasurementID string

	// AccountID is the target account for this position.
	AccountID string

	// InstrumentCode is the instrument identifier (e.g., "KWH", "USD").
	InstrumentCode string

	// InstrumentVersion is the version of the instrument definition (0 means use latest active).
	InstrumentVersion int

	// Amount is the decimal amount as a string for arbitrary precision.
	Amount string

	// BucketKey is the pre-computed or CEL-generated bucket key.
	BucketKey string

	// Timestamp is the parsed timestamp for this measurement.
	Timestamp time.Time

	// Attributes contains the key-value attributes extracted from CSV columns.
	Attributes map[string]string
}

// ValidatedRow represents a row that has passed validation and is ready for import.
type ValidatedRow struct {
	// Row is the original import row.
	Row *ImportRow

	// ResolvedInstrumentVersion is the actual version resolved during validation.
	// This is set when InstrumentVersion was 0 (use latest).
	ResolvedInstrumentVersion int

	// ComputedBucketKey is the bucket key computed during validation (if not pre-set).
	ComputedBucketKey string
}

// Summary contains statistics about the validation run.
type Summary struct {
	// TotalRows is the total number of rows processed.
	TotalRows int

	// ValidRows is the number of rows that passed all validation.
	ValidRows int

	// InvalidRows is the number of rows that failed validation.
	InvalidRows int

	// DuplicateCount is the number of duplicate rows detected.
	DuplicateCount int

	// MissingFieldCount is the number of missing required field errors.
	MissingFieldCount int

	// InstrumentNotFoundCount is the number of unknown instrument errors.
	InstrumentNotFoundCount int

	// SchemaErrorCount is the number of attribute schema validation errors.
	SchemaErrorCount int

	// InstrumentsCreated is the number of instruments auto-created (if enabled).
	InstrumentsCreated int

	// BloomFilterFalsePositives is the number of bloom filter hits that were not actual duplicates.
	// Used for monitoring bloom filter effectiveness.
	BloomFilterFalsePositives int

	// InstrumentCacheHits is the number of instrument cache hits.
	InstrumentCacheHits int

	// InstrumentCacheMisses is the number of instrument cache misses.
	InstrumentCacheMisses int

	// Duration is how long validation took.
	Duration time.Duration
}

// CacheHitRate returns the instrument cache hit rate as a percentage.
func (s *Summary) CacheHitRate() float64 {
	total := s.InstrumentCacheHits + s.InstrumentCacheMisses
	if total == 0 {
		return 0
	}
	return float64(s.InstrumentCacheHits) / float64(total) * 100
}

// BloomFilterEffectiveness returns the percentage of bloom filter hits that were true positives.
func (s *Summary) BloomFilterEffectiveness() float64 {
	totalHits := s.DuplicateCount + s.BloomFilterFalsePositives
	if totalHits == 0 {
		return 100 // No hits means 100% effectiveness (no false positives)
	}
	return float64(s.DuplicateCount) / float64(totalHits) * 100
}
