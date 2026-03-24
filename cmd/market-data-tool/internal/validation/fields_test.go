package validation

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
