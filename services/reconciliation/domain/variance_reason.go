package domain

// VarianceReason classifies the type of discrepancy found.
type VarianceReason string

// Supported variance reasons.
const (
	VarianceReasonAmountMismatch    VarianceReason = "AMOUNT_MISMATCH"
	VarianceReasonMissingEntry      VarianceReason = "MISSING_ENTRY"
	VarianceReasonDuplicateEntry    VarianceReason = "DUPLICATE_ENTRY"
	VarianceReasonTimingDifference  VarianceReason = "TIMING_DIFFERENCE"
	VarianceReasonCurrencyMismatch  VarianceReason = "CURRENCY_MISMATCH"
	VarianceReasonDirectionError    VarianceReason = "DIRECTION_ERROR"
	VarianceReasonQualityUpgrade    VarianceReason = "QUALITY_UPGRADE"
	VarianceReasonExternalMismatch  VarianceReason = "EXTERNAL_MISMATCH"
	VarianceReasonCorrectionApplied VarianceReason = "CORRECTION_APPLIED"
	VarianceReasonOther             VarianceReason = "OTHER"
)

// IsValid checks if the variance reason is a recognized value.
func (r VarianceReason) IsValid() bool {
	switch r {
	case VarianceReasonAmountMismatch, VarianceReasonMissingEntry,
		VarianceReasonDuplicateEntry, VarianceReasonTimingDifference,
		VarianceReasonCurrencyMismatch, VarianceReasonDirectionError,
		VarianceReasonQualityUpgrade, VarianceReasonExternalMismatch,
		VarianceReasonCorrectionApplied, VarianceReasonOther:
		return true
	}
	return false
}

// String returns the string representation.
func (r VarianceReason) String() string {
	return string(r)
}

// ParseVarianceReason converts a string to VarianceReason.
// Returns VarianceReasonOther for unrecognized values.
func ParseVarianceReason(s string) VarianceReason {
	reason := VarianceReason(s)
	if reason.IsValid() {
		return reason
	}
	return VarianceReasonOther
}
