package validation

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/infra"
)

func TestPipeline_ValidateRow(t *testing.T) {
	t.Run("validates valid row", func(t *testing.T) {
		pipeline := NewPipeline(PipelineConfig{})

		row := &ObservationRow{
			LineNumber:   2,
			DatasetCode:  "USD_EUR_FX",
			Value:        "1.0856",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
		}

		result := pipeline.ValidateRow(context.Background(), row)

		assert.False(t, result.HasErrors())
		assert.Equal(t, 2, result.LineNumber)
	})

	t.Run("detects missing value", func(t *testing.T) {
		pipeline := NewPipeline(PipelineConfig{})

		row := &ObservationRow{
			LineNumber:   2,
			DatasetCode:  "USD_EUR_FX",
			Value:        "",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
		}

		result := pipeline.ValidateRow(context.Background(), row)

		require.True(t, result.HasErrors())
		assert.Len(t, result.Errors, 1)
		assert.Contains(t, result.Error(), "value")
	})

	t.Run("detects missing quality_level", func(t *testing.T) {
		pipeline := NewPipeline(PipelineConfig{})

		row := &ObservationRow{
			LineNumber:   2,
			DatasetCode:  "USD_EUR_FX",
			Value:        "1.0856",
			QualityLevel: "",
			ObservedAt:   time.Now(),
		}

		result := pipeline.ValidateRow(context.Background(), row)

		require.True(t, result.HasErrors())
		assert.Contains(t, result.Error(), "quality_level")
	})

	t.Run("detects missing observed_at", func(t *testing.T) {
		pipeline := NewPipeline(PipelineConfig{})

		row := &ObservationRow{
			LineNumber:   2,
			DatasetCode:  "USD_EUR_FX",
			Value:        "1.0856",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Time{}, // Zero time
		}

		result := pipeline.ValidateRow(context.Background(), row)

		require.True(t, result.HasErrors())
		assert.Contains(t, result.Error(), "observed_at")
	})

	t.Run("tracks statistics correctly", func(t *testing.T) {
		pipeline := NewPipeline(PipelineConfig{})

		// Validate a valid row
		validRow := &ObservationRow{
			LineNumber:   2,
			Value:        "1.0856",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
		}
		pipeline.ValidateRow(context.Background(), validRow)

		// Validate an invalid row
		invalidRow := &ObservationRow{
			LineNumber:   3,
			Value:        "",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
		}
		pipeline.ValidateRow(context.Background(), invalidRow)

		summary := pipeline.Summary()
		assert.Equal(t, 2, summary.TotalRows)
		assert.Equal(t, 1, summary.ValidRows)
		assert.Equal(t, 1, summary.InvalidRows)
		assert.Equal(t, 1, summary.MissingFieldCount)
	})

	t.Run("runs CEL preview when configured", func(t *testing.T) {
		celPreview := infra.NewCELPreview("value != ''")

		pipeline := NewPipeline(PipelineConfig{
			CELPreview: celPreview,
		})

		row := &ObservationRow{
			LineNumber:   2,
			Value:        "1.0856",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
		}

		result := pipeline.ValidateRow(context.Background(), row)

		assert.False(t, result.HasErrors())
	})
}

func TestPipeline_ValidateBatch(t *testing.T) {
	pipeline := NewPipeline(PipelineConfig{})

	rows := []ObservationRow{
		{
			LineNumber:   2,
			Value:        "1.0856",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
		},
		{
			LineNumber:   3,
			Value:        "", // Invalid
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
		},
		{
			LineNumber:   4,
			Value:        "1.0860",
			QualityLevel: "ESTIMATE",
			ObservedAt:   time.Now(),
		},
	}

	results := pipeline.ValidateBatch(context.Background(), rows)

	// Only invalid rows should be in the results
	assert.Len(t, results, 1)
	assert.Contains(t, results, 3)
}

func TestSchemaValidator(t *testing.T) {
	t.Run("validates valid attributes", func(t *testing.T) {
		schema := `{
			"type": "object",
			"properties": {
				"tenor": {"type": "string"},
				"settlement_type": {"type": "string"}
			},
			"required": ["tenor"]
		}`

		validator := NewSchemaValidatorFromJSON(schema)
		require.NotNil(t, validator)

		attrs := map[string]string{
			"tenor":           "1M",
			"settlement_type": "T+2",
		}

		err := validator.Validate(attrs)
		assert.NoError(t, err)
	})

	t.Run("detects missing required attribute", func(t *testing.T) {
		schema := `{
			"type": "object",
			"properties": {
				"tenor": {"type": "string"}
			},
			"required": ["tenor"]
		}`

		validator := NewSchemaValidatorFromJSON(schema)
		require.NotNil(t, validator)

		attrs := map[string]string{
			"other_field": "value",
		}

		err := validator.Validate(attrs)
		require.Error(t, err)
	})

	t.Run("returns nil for nil validator", func(t *testing.T) {
		var validator *SchemaValidator

		err := validator.Validate(map[string]string{"foo": "bar"})
		assert.NoError(t, err)
	})

	t.Run("returns nil for empty schema", func(t *testing.T) {
		validator := NewSchemaValidatorFromJSON("")
		assert.Nil(t, validator)
	})
}

func TestFieldValidator(t *testing.T) {
	t.Run("validates complete row", func(t *testing.T) {
		validator := NewFieldValidator()

		row := &ObservationRow{
			Value:        "1.0856",
			QualityLevel: "ACTUAL",
			ObservedAt:   time.Now(),
		}

		errors := validator.Validate(row)
		assert.Empty(t, errors)
	})

	t.Run("detects multiple missing fields", func(t *testing.T) {
		validator := NewFieldValidator()

		row := &ObservationRow{
			Value:        "",
			QualityLevel: "",
			ObservedAt:   time.Time{},
		}

		errors := validator.Validate(row)
		assert.Len(t, errors, 3)
	})
}
