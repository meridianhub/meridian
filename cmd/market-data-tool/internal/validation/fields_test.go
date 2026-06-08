package validation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/services/market-information/domain"
)

func TestParseQualityString(t *testing.T) {
	t.Run("canonical four-level grades map to revision 0", func(t *testing.T) {
		cases := []struct {
			input string
			level domain.QualityLevel
		}{
			{"ESTIMATE", domain.QualityLevelEstimate},
			{"PROVISIONAL", domain.QualityLevelProvisional},
			{"ACTUAL", domain.QualityLevelActual},
			{"VERIFIED", domain.QualityLevelVerified},
		}
		for _, tc := range cases {
			level, revision, err := ParseQualityString(tc.input)
			require.NoError(t, err, "input: %s", tc.input)
			assert.Equal(t, tc.level, level, "input: %s", tc.input)
			assert.Equal(t, 0, revision, "input: %s", tc.input)
		}
	})

	t.Run("legacy REVISED maps to VERIFIED with revision 1", func(t *testing.T) {
		level, revision, err := ParseQualityString("REVISED")
		require.NoError(t, err)
		assert.Equal(t, domain.QualityLevelVerified, level)
		assert.Equal(t, 1, revision)
	})

	t.Run("matching is case-insensitive", func(t *testing.T) {
		cases := []string{"estimate", "Provisional", "actual", "verified", "revised", "vErIfIeD"}
		for _, input := range cases {
			_, _, err := ParseQualityString(input)
			require.NoError(t, err, "input: %s", input)
		}

		level, revision, err := ParseQualityString("revised")
		require.NoError(t, err)
		assert.Equal(t, domain.QualityLevelVerified, level)
		assert.Equal(t, 1, revision)
	})

	t.Run("surrounding whitespace is trimmed", func(t *testing.T) {
		level, revision, err := ParseQualityString("  PROVISIONAL\t")
		require.NoError(t, err)
		assert.Equal(t, domain.QualityLevelProvisional, level)
		assert.Equal(t, 0, revision)
	})

	t.Run("unknown values return ErrUnknownQualityString", func(t *testing.T) {
		cases := []string{"", "COEFFICIENT", "BOGUS", "estimated"}
		for _, input := range cases {
			level, revision, err := ParseQualityString(input)
			require.ErrorIs(t, err, ErrUnknownQualityString, "input: %q", input)
			assert.Equal(t, domain.QualityLevel(0), level, "input: %q", input)
			assert.Equal(t, 0, revision, "input: %q", input)
		}
	})
}

func TestDefaultRequiredFields(t *testing.T) {
	assert.Contains(t, DefaultRequiredFields, "value")
	assert.Contains(t, DefaultRequiredFields, "quality_level")
	assert.Contains(t, DefaultRequiredFields, "observed_at")
}

func TestNewFieldValidator(t *testing.T) {
	v := NewFieldValidator()
	require.NotNil(t, v)
	assert.Equal(t, DefaultRequiredFields, v.RequiredFields())
}

func TestNewFieldValidatorWithFields(t *testing.T) {
	custom := []string{"field_a", "field_b"}
	v := NewFieldValidatorWithFields(custom)
	require.NotNil(t, v)
	assert.Equal(t, custom, v.RequiredFields())
}

func TestFieldValidator_Validate(t *testing.T) {
	v := NewFieldValidator()

	t.Run("nil row returns field error", func(t *testing.T) {
		errs := v.Validate(nil)
		require.Len(t, errs, 1)
		assert.Equal(t, "row", errs[0].Field)
	})

	t.Run("complete row has no errors", func(t *testing.T) {
		row := &ObservationRow{
			Value:        "42.5",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
		}
		errs := v.Validate(row)
		assert.Empty(t, errs)
	})

	t.Run("missing value is reported", func(t *testing.T) {
		row := &ObservationRow{
			Value:        "",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
		}
		errs := v.Validate(row)
		require.Len(t, errs, 1)
		assert.Equal(t, "value", errs[0].Field)
		assert.Contains(t, errs[0].Error(), "empty")
	})

	t.Run("missing quality_level is reported", func(t *testing.T) {
		row := &ObservationRow{
			Value:        "1.0",
			QualityLevel: "",
			ObservedAt:   time.Now(),
		}
		errs := v.Validate(row)
		require.Len(t, errs, 1)
		assert.Equal(t, "quality_level", errs[0].Field)
	})

	t.Run("zero observed_at is reported", func(t *testing.T) {
		row := &ObservationRow{
			Value:        "1.0",
			QualityLevel: "ESTIMATE",
			ObservedAt:   time.Time{},
		}
		errs := v.Validate(row)
		require.Len(t, errs, 1)
		assert.Equal(t, "observed_at", errs[0].Field)
	})

	t.Run("all fields missing reports three errors", func(t *testing.T) {
		row := &ObservationRow{}
		errs := v.Validate(row)
		assert.Len(t, errs, 3)

		fields := make([]string, 0, len(errs))
		for _, e := range errs {
			fields = append(fields, e.Field)
		}
		assert.Contains(t, fields, "value")
		assert.Contains(t, fields, "quality_level")
		assert.Contains(t, fields, "observed_at")
	})
}

func TestFieldError_Error(t *testing.T) {
	t.Run("formats message without value", func(t *testing.T) {
		e := &FieldError{Field: "tenor", Reason: "required field is empty"}
		msg := e.Error()
		assert.Contains(t, msg, "tenor")
		assert.Contains(t, msg, "required field is empty")
		assert.NotContains(t, msg, "value:")
	})

	t.Run("formats message with value", func(t *testing.T) {
		e := &FieldError{Field: "quality_level", Value: "UNKNOWN", Reason: "invalid enum value"}
		msg := e.Error()
		assert.Contains(t, msg, "quality_level")
		assert.Contains(t, msg, "UNKNOWN")
		assert.Contains(t, msg, "invalid enum value")
	})
}
