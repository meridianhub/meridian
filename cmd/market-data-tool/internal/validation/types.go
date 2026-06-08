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

	// QualityLevel is the confidence grade on Axis A of the two-axis quality
	// model (ADR-0017): ESTIMATE, PROVISIONAL, ACTUAL, or VERIFIED. The legacy
	// REVISED label is accepted on input and normalizes to VERIFIED confidence
	// with Revision 1 (see validation.ParseQualityString).
	QualityLevel string

	// Revision is the correction counter on Axis B of the two-axis quality model:
	// 0 = original observation, 1+ = correction. Derived from QualityLevel via
	// ParseQualityString (only the legacy REVISED label yields a non-zero value).
	Revision int

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
