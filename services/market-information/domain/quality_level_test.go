package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQualityLevel_IsValid(t *testing.T) {
	tests := []struct {
		name  string
		level QualityLevel
		want  bool
	}{
		{
			name:  "ESTIMATE is valid",
			level: QualityLevelEstimate,
			want:  true,
		},
		{
			name:  "PROVISIONAL is valid",
			level: QualityLevelProvisional,
			want:  true,
		},
		{
			name:  "ACTUAL is valid",
			level: QualityLevelActual,
			want:  true,
		},
		{
			name:  "VERIFIED is valid",
			level: QualityLevelVerified,
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.level.IsValid())
		})
	}
}

func TestQualityLevel_IsValid_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		level QualityLevel
	}{
		{
			name:  "zero is invalid",
			level: QualityLevel(0),
		},
		{
			name:  "negative is invalid",
			level: QualityLevel(-1),
		},
		{
			name:  "five is invalid (above range)",
			level: QualityLevel(5),
		},
		{
			name:  "large number is invalid",
			level: QualityLevel(100),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, tt.level.IsValid())
		})
	}
}

func TestQualityLevel_String(t *testing.T) {
	tests := []struct {
		name  string
		level QualityLevel
		want  string
	}{
		{
			name:  "ESTIMATE string",
			level: QualityLevelEstimate,
			want:  "Estimate",
		},
		{
			name:  "PROVISIONAL string",
			level: QualityLevelProvisional,
			want:  "Provisional",
		},
		{
			name:  "ACTUAL string",
			level: QualityLevelActual,
			want:  "Actual",
		},
		{
			name:  "VERIFIED string",
			level: QualityLevelVerified,
			want:  "Verified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.level.String())
		})
	}
}

func TestQualityLevel_String_Invalid(t *testing.T) {
	tests := []struct {
		name  string
		level QualityLevel
	}{
		{"zero", QualityLevel(0)},
		{"negative", QualityLevel(-1)},
		{"above range", QualityLevel(5)},
		{"large number", QualityLevel(999)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, "Unknown", tt.level.String())
		})
	}
}

func TestQualityLevel_Constants(t *testing.T) {
	// Verify the constant values are as expected for the four-level ladder.
	assert.Equal(t, QualityLevel(1), QualityLevelEstimate)
	assert.Equal(t, QualityLevel(2), QualityLevelProvisional)
	assert.Equal(t, QualityLevel(3), QualityLevelActual)
	assert.Equal(t, QualityLevel(4), QualityLevelVerified)
}

func TestQualityLevel_Ordering(t *testing.T) {
	// Verify the natural ordering: ESTIMATE < PROVISIONAL < ACTUAL < VERIFIED
	assert.True(t, QualityLevelEstimate < QualityLevelProvisional,
		"ESTIMATE should be less than PROVISIONAL")
	assert.True(t, QualityLevelProvisional < QualityLevelActual,
		"PROVISIONAL should be less than ACTUAL")
	assert.True(t, QualityLevelActual < QualityLevelVerified,
		"ACTUAL should be less than VERIFIED")
	assert.True(t, QualityLevelEstimate < QualityLevelVerified,
		"ESTIMATE should be less than VERIFIED")
}

func TestQualityLevel_Supersedes(t *testing.T) {
	tests := []struct {
		name     string
		level    QualityLevel
		other    QualityLevel
		expected bool
	}{
		// VERIFIED supersedes everything below it.
		{"VERIFIED supersedes ACTUAL", QualityLevelVerified, QualityLevelActual, true},
		{"VERIFIED supersedes PROVISIONAL", QualityLevelVerified, QualityLevelProvisional, true},
		{"VERIFIED supersedes ESTIMATE", QualityLevelVerified, QualityLevelEstimate, true},
		// ACTUAL supersedes lower grades but not VERIFIED.
		{"ACTUAL supersedes PROVISIONAL", QualityLevelActual, QualityLevelProvisional, true},
		{"ACTUAL supersedes ESTIMATE", QualityLevelActual, QualityLevelEstimate, true},
		{"ACTUAL does not supersede VERIFIED", QualityLevelActual, QualityLevelVerified, false},
		// PROVISIONAL supersedes ESTIMATE only.
		{"PROVISIONAL supersedes ESTIMATE", QualityLevelProvisional, QualityLevelEstimate, true},
		{"PROVISIONAL does not supersede ACTUAL", QualityLevelProvisional, QualityLevelActual, false},
		{"PROVISIONAL does not supersede VERIFIED", QualityLevelProvisional, QualityLevelVerified, false},
		// ESTIMATE supersedes nothing.
		{"ESTIMATE does not supersede PROVISIONAL", QualityLevelEstimate, QualityLevelProvisional, false},
		{"ESTIMATE does not supersede ACTUAL", QualityLevelEstimate, QualityLevelActual, false},
		{"ESTIMATE does not supersede VERIFIED", QualityLevelEstimate, QualityLevelVerified, false},
		// Same level never supersedes itself.
		{"ESTIMATE does not supersede itself", QualityLevelEstimate, QualityLevelEstimate, false},
		{"PROVISIONAL does not supersede itself", QualityLevelProvisional, QualityLevelProvisional, false},
		{"ACTUAL does not supersede itself", QualityLevelActual, QualityLevelActual, false},
		{"VERIFIED does not supersede itself", QualityLevelVerified, QualityLevelVerified, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.level.Supersedes(tt.other))
		})
	}
}

// TestQualityLevel_Supersedes_AxisAOnly proves Supersedes ranks purely on the
// confidence grade (Axis A). A correction/revision is an Axis-B concern tracked
// by the revision counter and supersession links, never by the enum. So a
// low-confidence row that happens to be a later correction must NOT outrank a
// higher-confidence row via Supersedes.
func TestQualityLevel_Supersedes_AxisAOnly(t *testing.T) {
	// Imagine a PROVISIONAL observation that is itself a correction (revision >= 1)
	// of an earlier reading. Its lifecycle state is irrelevant to Supersedes: it
	// still must not outrank an ACTUAL or VERIFIED value on confidence alone.
	revisedLowConfidence := QualityLevelProvisional

	assert.False(t, revisedLowConfidence.Supersedes(QualityLevelActual),
		"a revised low-confidence row must not outrank a higher-confidence ACTUAL via Supersedes")
	assert.False(t, revisedLowConfidence.Supersedes(QualityLevelVerified),
		"a revised low-confidence row must not outrank a higher-confidence VERIFIED via Supersedes")

	// And the reverse holds: higher confidence still supersedes regardless of any
	// lifecycle/correction state the lower row might carry.
	assert.True(t, QualityLevelVerified.Supersedes(revisedLowConfidence),
		"a higher-confidence VERIFIED supersedes a lower-confidence row irrespective of revision state")
}

func TestQualityLevel_Int(t *testing.T) {
	tests := []struct {
		name  string
		level QualityLevel
		want  int
	}{
		{"ESTIMATE int value", QualityLevelEstimate, 1},
		{"PROVISIONAL int value", QualityLevelProvisional, 2},
		{"ACTUAL int value", QualityLevelActual, 3},
		{"VERIFIED int value", QualityLevelVerified, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.level.Int())
		})
	}
}

func TestQualityLevel_AllValidLevelsCount(t *testing.T) {
	// Test that we have exactly 4 valid quality levels.
	validLevels := []QualityLevel{
		QualityLevelEstimate,
		QualityLevelProvisional,
		QualityLevelActual,
		QualityLevelVerified,
	}

	for _, level := range validLevels {
		assert.True(t, level.IsValid(), "expected %d to be valid", level)
	}

	assert.Len(t, validLevels, 4, "expected exactly 4 valid quality levels")
}

func TestQualityLevel_ComparisonOperators(t *testing.T) {
	// Test direct comparison using Go operators
	tests := []struct {
		name     string
		a, b     QualityLevel
		lessEq   bool
		greater  bool
		lessOnly bool
		equal    bool
	}{
		{
			name:     "ESTIMATE vs PROVISIONAL",
			a:        QualityLevelEstimate,
			b:        QualityLevelProvisional,
			lessEq:   true,
			greater:  false,
			lessOnly: true,
			equal:    false,
		},
		{
			name:     "PROVISIONAL vs ACTUAL",
			a:        QualityLevelProvisional,
			b:        QualityLevelActual,
			lessEq:   true,
			greater:  false,
			lessOnly: true,
			equal:    false,
		},
		{
			name:     "ACTUAL vs VERIFIED",
			a:        QualityLevelActual,
			b:        QualityLevelVerified,
			lessEq:   true,
			greater:  false,
			lessOnly: true,
			equal:    false,
		},
		{
			name:     "VERIFIED vs ESTIMATE",
			a:        QualityLevelVerified,
			b:        QualityLevelEstimate,
			lessEq:   false,
			greater:  true,
			lessOnly: false,
			equal:    false,
		},
		{
			name:     "ACTUAL vs ACTUAL",
			a:        QualityLevelActual,
			b:        QualityLevelActual,
			lessEq:   true,
			greater:  false,
			lessOnly: false,
			equal:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.lessEq, tt.a <= tt.b, "expected %d <= %d to be %v", tt.a, tt.b, tt.lessEq)
			assert.Equal(t, tt.greater, tt.a > tt.b, "expected %d > %d to be %v", tt.a, tt.b, tt.greater)
			assert.Equal(t, tt.lessOnly, tt.a < tt.b, "expected %d < %d to be %v", tt.a, tt.b, tt.lessOnly)
			assert.Equal(t, tt.equal, tt.a == tt.b, "expected %d == %d to be %v", tt.a, tt.b, tt.equal)
		})
	}
}

func TestQualityLevel_UsedInSwitch(t *testing.T) {
	// Test that quality levels work correctly in switch statements
	getLevelDescription := func(level QualityLevel) string {
		switch level {
		case QualityLevelEstimate:
			return "estimated"
		case QualityLevelProvisional:
			return "provisional"
		case QualityLevelActual:
			return "actual"
		case QualityLevelVerified:
			return "verified"
		default:
			return "unknown"
		}
	}

	assert.Equal(t, "estimated", getLevelDescription(QualityLevelEstimate))
	assert.Equal(t, "provisional", getLevelDescription(QualityLevelProvisional))
	assert.Equal(t, "actual", getLevelDescription(QualityLevelActual))
	assert.Equal(t, "verified", getLevelDescription(QualityLevelVerified))
	assert.Equal(t, "unknown", getLevelDescription(QualityLevel(0)))
	assert.Equal(t, "unknown", getLevelDescription(QualityLevel(99)))
}

func TestQualityLevel_UsedAsMapKey(t *testing.T) {
	// Test that quality levels can be used as map keys
	descriptions := map[QualityLevel]string{
		QualityLevelEstimate:    "Forecasted value",
		QualityLevelProvisional: "Unvalidated measured value",
		QualityLevelActual:      "Measured value",
		QualityLevelVerified:    "Validated value",
	}

	assert.Equal(t, "Forecasted value", descriptions[QualityLevelEstimate])
	assert.Equal(t, "Unvalidated measured value", descriptions[QualityLevelProvisional])
	assert.Equal(t, "Measured value", descriptions[QualityLevelActual])
	assert.Equal(t, "Validated value", descriptions[QualityLevelVerified])

	// Invalid key returns zero value
	_, exists := descriptions[QualityLevel(0)]
	assert.False(t, exists)
}
