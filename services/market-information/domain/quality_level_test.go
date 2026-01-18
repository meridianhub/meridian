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
			name:  "four is invalid (above range)",
			level: QualityLevel(4),
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
		{"above range", QualityLevel(4)},
		{"large number", QualityLevel(999)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, "Unknown", tt.level.String())
		})
	}
}

func TestQualityLevel_Constants(t *testing.T) {
	// Verify the constant values are as expected
	assert.Equal(t, QualityLevel(1), QualityLevelEstimate)
	assert.Equal(t, QualityLevel(2), QualityLevelActual)
	assert.Equal(t, QualityLevel(3), QualityLevelVerified)
}

func TestQualityLevel_Ordering(t *testing.T) {
	// Verify the natural ordering: ESTIMATE < ACTUAL < VERIFIED
	assert.True(t, QualityLevelEstimate < QualityLevelActual,
		"ESTIMATE should be less than ACTUAL")
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
		// VERIFIED supersedes ACTUAL and ESTIMATE
		{
			name:     "VERIFIED supersedes ACTUAL",
			level:    QualityLevelVerified,
			other:    QualityLevelActual,
			expected: true,
		},
		{
			name:     "VERIFIED supersedes ESTIMATE",
			level:    QualityLevelVerified,
			other:    QualityLevelEstimate,
			expected: true,
		},
		// ACTUAL supersedes ESTIMATE but not VERIFIED
		{
			name:     "ACTUAL supersedes ESTIMATE",
			level:    QualityLevelActual,
			other:    QualityLevelEstimate,
			expected: true,
		},
		{
			name:     "ACTUAL does not supersede VERIFIED",
			level:    QualityLevelActual,
			other:    QualityLevelVerified,
			expected: false,
		},
		// ESTIMATE doesn't supersede anything
		{
			name:     "ESTIMATE does not supersede ACTUAL",
			level:    QualityLevelEstimate,
			other:    QualityLevelActual,
			expected: false,
		},
		{
			name:     "ESTIMATE does not supersede VERIFIED",
			level:    QualityLevelEstimate,
			other:    QualityLevelVerified,
			expected: false,
		},
		// Same level does not supersede itself
		{
			name:     "ESTIMATE does not supersede itself",
			level:    QualityLevelEstimate,
			other:    QualityLevelEstimate,
			expected: false,
		},
		{
			name:     "ACTUAL does not supersede itself",
			level:    QualityLevelActual,
			other:    QualityLevelActual,
			expected: false,
		},
		{
			name:     "VERIFIED does not supersede itself",
			level:    QualityLevelVerified,
			other:    QualityLevelVerified,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.level.Supersedes(tt.other))
		})
	}
}

func TestQualityLevel_Int(t *testing.T) {
	tests := []struct {
		name  string
		level QualityLevel
		want  int
	}{
		{
			name:  "ESTIMATE int value",
			level: QualityLevelEstimate,
			want:  1,
		},
		{
			name:  "ACTUAL int value",
			level: QualityLevelActual,
			want:  2,
		},
		{
			name:  "VERIFIED int value",
			level: QualityLevelVerified,
			want:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.level.Int())
		})
	}
}

func TestQualityLevel_AllValidLevelsCount(t *testing.T) {
	// Test that we have exactly 3 valid quality levels
	validLevels := []QualityLevel{
		QualityLevelEstimate,
		QualityLevelActual,
		QualityLevelVerified,
	}

	for _, level := range validLevels {
		assert.True(t, level.IsValid(), "expected %d to be valid", level)
	}

	assert.Len(t, validLevels, 3, "expected exactly 3 valid quality levels")
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
			name:     "ESTIMATE vs ACTUAL",
			a:        QualityLevelEstimate,
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
		case QualityLevelActual:
			return "actual"
		case QualityLevelVerified:
			return "verified"
		default:
			return "unknown"
		}
	}

	assert.Equal(t, "estimated", getLevelDescription(QualityLevelEstimate))
	assert.Equal(t, "actual", getLevelDescription(QualityLevelActual))
	assert.Equal(t, "verified", getLevelDescription(QualityLevelVerified))
	assert.Equal(t, "unknown", getLevelDescription(QualityLevel(0)))
	assert.Equal(t, "unknown", getLevelDescription(QualityLevel(99)))
}

func TestQualityLevel_UsedAsMapKey(t *testing.T) {
	// Test that quality levels can be used as map keys
	descriptions := map[QualityLevel]string{
		QualityLevelEstimate: "Forecasted value",
		QualityLevelActual:   "Measured value",
		QualityLevelVerified: "Validated value",
	}

	assert.Equal(t, "Forecasted value", descriptions[QualityLevelEstimate])
	assert.Equal(t, "Measured value", descriptions[QualityLevelActual])
	assert.Equal(t, "Validated value", descriptions[QualityLevelVerified])

	// Invalid key returns zero value
	_, exists := descriptions[QualityLevel(0)]
	assert.False(t, exists)
}
