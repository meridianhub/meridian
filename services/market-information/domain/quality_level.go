// Package domain contains the core business logic for market information.
package domain

// QualityLevel represents the confidence grade of market data on Axis A of the
// two-axis quality model (ADR-0017). Higher values indicate greater confidence
// and take precedence during reconciliation.
//
// The four-level confidence ladder:
//   - ESTIMATE (1): Forecasted or projected values based on models/patterns.
//   - PROVISIONAL (2): Metered/received data that has not yet been validated.
//   - ACTUAL (3): Metered, validated data.
//   - VERIFIED (4): Actual values that have been cross-checked against multiple
//     sources or manually verified.
//
// This ordering drives the Estimates vs. Actuals reconciliation pattern: when
// multiple values exist for the same time period, higher confidence grades
// supersede lower ones via Supersedes.
//
// Axis A (confidence, this enum) is orthogonal to Axis B (lifecycle/correction
// lineage), which is tracked by the revision counter and supersession links, not
// by a confidence grade.
type QualityLevel int

// Quality level constants for the Axis-A confidence ladder.
const (
	// QualityLevelEstimate represents forecasted or projected values.
	// These are typically generated from models, historical patterns, or forward curves.
	QualityLevelEstimate QualityLevel = 1

	// QualityLevelProvisional represents metered or received data that has not yet
	// been validated. It sits above ESTIMATE (real measurement, not a forecast) but
	// below ACTUAL (not yet validated).
	QualityLevelProvisional QualityLevel = 2

	// QualityLevelActual represents metered, validated data from data sources.
	// These are received from APIs, feeds, or manual entry of observed data.
	QualityLevelActual QualityLevel = 3

	// QualityLevelVerified represents actual values that have been cross-checked.
	// These have undergone validation against multiple sources or manual verification.
	QualityLevelVerified QualityLevel = 4
)

// qualityLevelNames maps quality levels to their display names.
var qualityLevelNames = map[QualityLevel]string{
	QualityLevelEstimate:    "Estimate",
	QualityLevelProvisional: "Provisional",
	QualityLevelActual:      "Actual",
	QualityLevelVerified:    "Verified",
}

// validQualityLevels contains all valid quality levels for efficient lookup.
var validQualityLevels = map[QualityLevel]bool{
	QualityLevelEstimate:    true,
	QualityLevelProvisional: true,
	QualityLevelActual:      true,
	QualityLevelVerified:    true,
}

// String returns the human-readable name of the quality level.
// Returns "Unknown" for invalid quality levels.
func (q QualityLevel) String() string {
	if name, ok := qualityLevelNames[q]; ok {
		return name
	}
	return "Unknown"
}

// IsValid returns true if the quality level is a recognized valid level.
func (q QualityLevel) IsValid() bool {
	return validQualityLevels[q]
}

// Supersedes returns true if this quality level should take precedence over the
// other. This is Axis A only: a higher confidence grade supersedes a lower one.
// Lifecycle/correction state (Axis B) is handled separately by the revision
// counter and supersession links and must not influence this comparison.
func (q QualityLevel) Supersedes(other QualityLevel) bool {
	return q > other
}

// Int returns the integer value of the quality level.
func (q QualityLevel) Int() int {
	return int(q)
}
