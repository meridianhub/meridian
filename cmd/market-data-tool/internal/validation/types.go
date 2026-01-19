package validation

import (
	"time"
)

// ObservationRow represents a single row being validated.
type ObservationRow struct {
	// LineNumber is the 1-indexed line number in the source CSV.
	LineNumber int

	// DatasetCode is the target dataset code.
	DatasetCode string

	// Value is the observed value as a string.
	Value string

	// ObservedAt is when the observation was made.
	ObservedAt time.Time

	// ValidFrom is when the observation value becomes valid (optional).
	ValidFrom *time.Time

	// ValidTo is when the observation value expires (optional).
	ValidTo *time.Time

	// QualityLevel is the quality indicator (ESTIMATE, PROVISIONAL, ACTUAL, REVISED).
	QualityLevel string

	// Attributes contains the key-value attributes extracted from CSV columns.
	Attributes map[string]string
}

// ValidatedRow represents a row that has passed validation and is ready for import.
type ValidatedRow struct {
	// Row is the original observation row.
	Row *ObservationRow

	// ResolvedDatasetVersion is the actual version resolved during validation.
	ResolvedDatasetVersion int32

	// ComputedResolutionKey is the resolution key computed from the CEL expression.
	ComputedResolutionKey string
}
