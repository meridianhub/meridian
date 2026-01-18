// Package domain contains the core business logic for market information.
package domain

// QualityLevel represents the quality tier of market data in the Time-Bound Quality Ladder.
// Higher values indicate more reliable data that should take precedence in reconciliation.
//
// The quality ladder follows this hierarchy:
//   - ESTIMATE (1): Forecasted or projected values based on models/patterns
//   - ACTUAL (2): Real measured values from data sources
//   - VERIFIED (3): Actual values that have been cross-checked and validated
//
// This ordering is fundamental to the Estimates vs. Actuals reconciliation pattern:
// when multiple values exist for the same time period, higher quality levels supersede lower ones.
type QualityLevel int

// Quality level constants for the Time-Bound Quality Ladder.
const (
	// QualityLevelEstimate represents forecasted or projected values.
	// These are typically generated from models, historical patterns, or forward curves.
	QualityLevelEstimate QualityLevel = 1

	// QualityLevelActual represents real measured values from data sources.
	// These are received from APIs, feeds, or manual entry of observed data.
	QualityLevelActual QualityLevel = 2

	// QualityLevelVerified represents actual values that have been cross-checked.
	// These have undergone validation against multiple sources or manual verification.
	QualityLevelVerified QualityLevel = 3
)

// qualityLevelNames maps quality levels to their display names.
var qualityLevelNames = map[QualityLevel]string{
	QualityLevelEstimate: "Estimate",
	QualityLevelActual:   "Actual",
	QualityLevelVerified: "Verified",
}

// validQualityLevels contains all valid quality levels for efficient lookup.
var validQualityLevels = map[QualityLevel]bool{
	QualityLevelEstimate: true,
	QualityLevelActual:   true,
	QualityLevelVerified: true,
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

// Supersedes returns true if this quality level should take precedence over the other.
// Higher quality levels supersede lower ones in the reconciliation process.
func (q QualityLevel) Supersedes(other QualityLevel) bool {
	return q > other
}

// Int returns the integer value of the quality level.
func (q QualityLevel) Int() int {
	return int(q)
}
