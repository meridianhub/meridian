package csvadapter

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/meridianhub/meridian/cmd/market-data-tool/internal/infra"
)

func TestParser_Parse(t *testing.T) {
	t.Run("parses valid CSV with required columns", func(t *testing.T) {
		csvData := `observed_at,quality_level,value
2024-01-15T10:30:00Z,ACTUAL,1.0856
2024-01-15T11:30:00Z,ESTIMATE,1.0860`

		dataset := &infra.DataSetDefinition{
			Code: "USD_EUR_FX",
		}

		parser := NewParser(dataset)
		var batches []RowBatch

		result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
			batches = append(batches, batch)
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 2, result.RowCount)
		assert.Equal(t, 0, result.ErrorCount)
		require.Len(t, batches, 1)
		require.Len(t, batches[0].Rows, 2)

		// Verify first row
		row1 := batches[0].Rows[0]
		assert.Equal(t, 2, row1.LineNumber)
		assert.Equal(t, "1.0856", row1.Value)
		assert.Equal(t, "ACTUAL", row1.QualityLevel)
		assert.Equal(t, 2024, row1.ObservedAt.Year())
	})

	t.Run("parses CSV with optional temporal bounds", func(t *testing.T) {
		csvData := `observed_at,quality_level,value,valid_from,valid_to
2024-01-15T10:30:00Z,ACTUAL,1.0856,2024-01-15T00:00:00Z,2024-01-16T00:00:00Z`

		dataset := &infra.DataSetDefinition{Code: "USD_EUR_FX"}
		parser := NewParser(dataset)
		var batches []RowBatch

		result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
			batches = append(batches, batch)
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 1, result.RowCount)
		require.Len(t, batches, 1)
		require.Len(t, batches[0].Rows, 1)

		row := batches[0].Rows[0]
		require.NotNil(t, row.ValidFrom)
		require.NotNil(t, row.ValidTo)
		assert.Equal(t, 15, row.ValidFrom.Day())
		assert.Equal(t, 16, row.ValidTo.Day())
	})

	t.Run("extracts dynamic attributes", func(t *testing.T) {
		csvData := `observed_at,quality_level,value,tenor,settlement_type
2024-01-15T10:30:00Z,ACTUAL,1.0856,1M,T+2`

		dataset := &infra.DataSetDefinition{Code: "INTEREST_RATE"}
		parser := NewParser(dataset)
		var batches []RowBatch

		result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
			batches = append(batches, batch)
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 1, result.RowCount)
		require.Len(t, batches[0].Rows, 1)

		row := batches[0].Rows[0]
		assert.Equal(t, "1M", row.Attributes["tenor"])
		assert.Equal(t, "T+2", row.Attributes["settlement_type"])
	})

	t.Run("normalizes quality levels", func(t *testing.T) {
		testCases := []struct {
			input    string
			expected string
		}{
			{"ACTUAL", "ACTUAL"},
			{"actual", "ACTUAL"},
			{"Actual", "ACTUAL"},
			{"ESTIMATE", "ESTIMATE"},
			{"estimate", "ESTIMATE"},
			{"PROVISIONAL", "PROVISIONAL"},
			{"VERIFIED", "VERIFIED"},
			{"verified", "VERIFIED"},
			{"Verified", "VERIFIED"},
			{"REVISED", "REVISED"},
		}

		for _, tc := range testCases {
			csvData := `observed_at,quality_level,value
2024-01-15T10:30:00Z,` + tc.input + `,1.0856`

			dataset := &infra.DataSetDefinition{Code: "TEST"}
			parser := NewParser(dataset)
			var batches []RowBatch

			result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
				batches = append(batches, batch)
				return nil
			})

			require.NoError(t, err, "input: %s", tc.input)
			assert.Equal(t, 1, result.RowCount)
			assert.Equal(t, tc.expected, batches[0].Rows[0].QualityLevel)
		}
	})

	t.Run("handles missing required columns", func(t *testing.T) {
		csvData := `observed_at,value
2024-01-15T10:30:00Z,1.0856`

		dataset := &infra.DataSetDefinition{Code: "TEST"}
		parser := NewParser(dataset)

		_, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(_ RowBatch) error {
			return nil
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrMissingHeader)
		assert.Contains(t, err.Error(), "quality_level")
	})

	t.Run("handles empty file", func(t *testing.T) {
		csvData := ``

		dataset := &infra.DataSetDefinition{Code: "TEST"}
		parser := NewParser(dataset)

		_, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(_ RowBatch) error {
			return nil
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, ErrEmptyFile)
	})

	t.Run("handles invalid timestamp", func(t *testing.T) {
		csvData := `observed_at,quality_level,value
invalid-timestamp,ACTUAL,1.0856`

		dataset := &infra.DataSetDefinition{Code: "TEST"}
		parser := NewParser(dataset)
		var batches []RowBatch

		result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
			batches = append(batches, batch)
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 0, result.RowCount)
		assert.Equal(t, 1, result.ErrorCount)
		require.Len(t, batches, 1)
		require.Len(t, batches[0].Errors, 1)
		assert.ErrorIs(t, batches[0].Errors[0].Err, ErrInvalidTimestamp)
	})

	t.Run("handles invalid quality level", func(t *testing.T) {
		csvData := `observed_at,quality_level,value
2024-01-15T10:30:00Z,INVALID,1.0856`

		dataset := &infra.DataSetDefinition{Code: "TEST"}
		parser := NewParser(dataset)
		var batches []RowBatch

		result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
			batches = append(batches, batch)
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 0, result.RowCount)
		assert.Equal(t, 1, result.ErrorCount)
		require.Len(t, batches[0].Errors, 1)
		assert.ErrorIs(t, batches[0].Errors[0].Err, ErrInvalidQualityLevel)
	})

	t.Run("handles value too long", func(t *testing.T) {
		longValue := strings.Repeat("1", 65)
		csvData := `observed_at,quality_level,value
2024-01-15T10:30:00Z,ACTUAL,` + longValue

		dataset := &infra.DataSetDefinition{Code: "TEST"}
		parser := NewParser(dataset)
		var batches []RowBatch

		result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
			batches = append(batches, batch)
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 0, result.RowCount)
		assert.Equal(t, 1, result.ErrorCount)
		require.Len(t, batches[0].Errors, 1)
		assert.ErrorIs(t, batches[0].Errors[0].Err, ErrValueTooLong)
	})

	t.Run("skips empty rows when configured", func(t *testing.T) {
		csvData := `observed_at,quality_level,value
2024-01-15T10:30:00Z,ACTUAL,1.0856
,,
2024-01-15T11:30:00Z,ACTUAL,1.0860`

		dataset := &infra.DataSetDefinition{Code: "TEST"}
		parser := NewParser(dataset)
		var batches []RowBatch

		config := DefaultParseConfig()
		config.SkipEmptyRows = true

		result, err := parser.Parse(context.Background(), strings.NewReader(csvData), config, func(batch RowBatch) error {
			batches = append(batches, batch)
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 2, result.RowCount)
		assert.Equal(t, 0, result.ErrorCount)
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		csvData := `observed_at,quality_level,value
2024-01-15T10:30:00Z,ACTUAL,1.0856
2024-01-15T11:30:00Z,ACTUAL,1.0860`

		dataset := &infra.DataSetDefinition{Code: "TEST"}
		parser := NewParser(dataset)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := parser.Parse(ctx, strings.NewReader(csvData), DefaultParseConfig(), func(_ RowBatch) error {
			return nil
		})

		require.Error(t, err)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("batches rows correctly", func(t *testing.T) {
		var rows []string
		rows = append(rows, "observed_at,quality_level,value")
		for i := 0; i < 25; i++ {
			rows = append(rows, "2024-01-15T10:30:00Z,ACTUAL,1.0856")
		}
		csvData := strings.Join(rows, "\n")

		dataset := &infra.DataSetDefinition{Code: "TEST"}
		parser := NewParser(dataset)
		var batchCount int
		var totalRows int

		config := ParseConfig{
			BatchSize:        10,
			SkipEmptyRows:    true,
			TimestampFormats: []string{time.RFC3339},
		}

		result, err := parser.Parse(context.Background(), strings.NewReader(csvData), config, func(batch RowBatch) error {
			batchCount++
			totalRows += len(batch.Rows)
			return nil
		})

		require.NoError(t, err)
		assert.Equal(t, 25, result.RowCount)
		assert.Equal(t, 3, batchCount) // 10 + 10 + 5
		assert.Equal(t, 25, totalRows)
	})
}

func TestNormalizeColumnName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"observed_at", "observed_at"},
		{"Observed_At", "observed_at"},
		{"OBSERVED_AT", "observed_at"},
		{"observed-at", "observed_at"},
		{"observed at", "observed_at"},
		{"  observed_at  ", "observed_at"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result := normalizeColumnName(tc.input)
			assert.Equal(t, tc.expected, result)
		})
	}
}
