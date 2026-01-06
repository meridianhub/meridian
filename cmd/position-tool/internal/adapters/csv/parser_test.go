package csv

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/meridianhub/meridian/pkg/platform/quantity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test sentinel errors.
var (
	errInstrumentNotFound = errors.New("instrument not found")
	errParseTest          = errors.New("parse error")
	errGeneralTest        = errors.New("general error")
)

// mockRegistry implements quantity.InstrumentRegistry for testing.
type mockRegistry struct {
	definitions map[string]*quantity.InstrumentDefinition
	err         error
}

func (m *mockRegistry) GetDefinition(_ context.Context, code string, _ int32) (*quantity.InstrumentDefinition, error) {
	if m.err != nil {
		return nil, m.err
	}
	def, ok := m.definitions[code]
	if !ok {
		return nil, errInstrumentNotFound
	}
	return def, nil
}

func (m *mockRegistry) GetActiveDefinition(ctx context.Context, code string) (*quantity.InstrumentDefinition, error) {
	return m.GetDefinition(ctx, code, 0)
}

func (m *mockRegistry) ListActive(_ context.Context) ([]*quantity.InstrumentDefinition, error) {
	return nil, nil
}

func (m *mockRegistry) CreateDraft(_ context.Context, _ *quantity.InstrumentDefinition) (*quantity.InstrumentDefinition, error) {
	return nil, nil
}

func (m *mockRegistry) ActivateInstrument(_ context.Context, _ string, _ int32) error {
	return nil
}

func (m *mockRegistry) DeprecateInstrument(_ context.Context, _ string, _ int32) error {
	return nil
}

func newMockRegistry() *mockRegistry {
	return &mockRegistry{
		definitions: map[string]*quantity.InstrumentDefinition{
			"KWH": {
				Code:                     "KWH",
				Version:                  1,
				Dimension:                "ENERGY",
				FungibilityKeyExpression: `bucket_key([attributes.grid_zone, attributes.tariff_code])`,
			},
			"USD": {
				Code:                     "USD",
				Version:                  1,
				Dimension:                "CURRENCY",
				FungibilityKeyExpression: "", // No attributes for fungible currency
			},
			"CARBON_CREDIT": {
				Code:                     "CARBON_CREDIT",
				Version:                  1,
				Dimension:                "CARBON",
				FungibilityKeyExpression: `bucket_key([attributes.vintage_year, attributes.certification_type, attributes.origin_country])`,
			},
		},
	}
}

func TestParser_Parse_ValidCSV(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	csvData := `account_id,instrument_code,amount,timestamp,grid_zone,tariff_code
acc-001,KWH,1234.56,2024-01-15T10:30:00Z,ZONE_A,PEAK
acc-002,KWH,789.01,2024-01-15T11:00:00Z,ZONE_B,OFF_PEAK
acc-001,KWH,456.78,2024-01-15T12:00:00Z,ZONE_A,STANDARD
`

	var batches []RowBatch
	result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
		batches = append(batches, batch)
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, 3, result.RowCount)
	assert.Equal(t, 0, result.ErrorCount)
	assert.Empty(t, result.Warnings)

	// Check schema
	require.NotNil(t, result.Schema)
	assert.Equal(t, "KWH", result.Schema.InstrumentCode)
	assert.Equal(t, int32(1), result.Schema.InstrumentVersion)
	assert.ElementsMatch(t, []string{"grid_zone", "tariff_code"}, result.Schema.AttributeKeys)

	// Check parsed rows
	require.Len(t, batches, 1)
	rows := batches[0].Rows
	require.Len(t, rows, 3)

	// First row
	assert.Equal(t, 2, rows[0].LineNumber)
	assert.Equal(t, "acc-001", rows[0].AccountID)
	assert.Equal(t, "KWH", rows[0].InstrumentCode)
	assert.Equal(t, "1234.56", rows[0].Amount)
	assert.Equal(t, "ZONE_A", rows[0].Attributes["grid_zone"])
	assert.Equal(t, "PEAK", rows[0].Attributes["tariff_code"])

	// Second row
	assert.Equal(t, 3, rows[1].LineNumber)
	assert.Equal(t, "acc-002", rows[1].AccountID)
	assert.Equal(t, "ZONE_B", rows[1].Attributes["grid_zone"])
	assert.Equal(t, "OFF_PEAK", rows[1].Attributes["tariff_code"])
}

func TestParser_Parse_MissingMandatoryColumn(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	// Missing timestamp column
	csvData := `account_id,instrument_code,amount,grid_zone,tariff_code
acc-001,KWH,1234.56,ZONE_A,PEAK
`

	_, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(_ RowBatch) error {
		return nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingHeader)
	assert.Contains(t, err.Error(), "timestamp")
}

func TestParser_Parse_MissingAttributeColumn(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	// Missing tariff_code which is required by CEL expression
	csvData := `account_id,instrument_code,amount,timestamp,grid_zone
acc-001,KWH,1234.56,2024-01-15T10:30:00Z,ZONE_A
`

	_, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(_ RowBatch) error {
		return nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingAttributeColumn)
	assert.Contains(t, err.Error(), "tariff_code")
}

func TestParser_Parse_ExtraColumns_Warning(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	// Extra column "notes" not in schema
	csvData := `account_id,instrument_code,amount,timestamp,grid_zone,tariff_code,notes
acc-001,KWH,1234.56,2024-01-15T10:30:00Z,ZONE_A,PEAK,some notes
`

	config := DefaultParseConfig()
	config.StrictHeaders = false

	result, err := parser.Parse(context.Background(), strings.NewReader(csvData), config, func(_ RowBatch) error {
		return nil
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Warnings, 1)
	assert.Contains(t, result.Warnings[0], "notes")
	assert.Contains(t, result.Schema.ExtraColumns, "notes")
}

func TestParser_Parse_ExtraColumns_StrictMode(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	csvData := `account_id,instrument_code,amount,timestamp,grid_zone,tariff_code,notes
acc-001,KWH,1234.56,2024-01-15T10:30:00Z,ZONE_A,PEAK,some notes
`

	config := DefaultParseConfig()
	config.StrictHeaders = true

	_, err := parser.Parse(context.Background(), strings.NewReader(csvData), config, func(_ RowBatch) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "strict mode")
}

func TestParser_Parse_InstrumentNotFound(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	csvData := `account_id,instrument_code,amount,timestamp
acc-001,UNKNOWN,1234.56,2024-01-15T10:30:00Z
`

	_, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(_ RowBatch) error {
		return nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInstrumentNotFound)
}

func TestParser_Parse_MixedInstruments(t *testing.T) {
	registry := newMockRegistry()
	// USD has no CEL attributes, so no extra columns needed
	parser := NewParser(registry)

	csvData := `account_id,instrument_code,amount,timestamp
acc-001,USD,100.00,2024-01-15T10:30:00Z
acc-002,EUR,200.00,2024-01-15T11:00:00Z
`

	_, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(_ RowBatch) error {
		return nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMixedInstruments)
}

func TestParser_Parse_EmptyFile(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	_, err := parser.Parse(context.Background(), strings.NewReader(""), DefaultParseConfig(), func(_ RowBatch) error {
		return nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyFile)
}

func TestParser_Parse_HeaderOnly(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	csvData := `account_id,instrument_code,amount,timestamp,grid_zone,tariff_code
`

	_, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(_ RowBatch) error {
		return nil
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrEmptyFile)
}

func TestParser_Parse_InvalidTimestamp(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	csvData := `account_id,instrument_code,amount,timestamp,grid_zone,tariff_code
acc-001,KWH,1234.56,not-a-timestamp,ZONE_A,PEAK
acc-002,KWH,789.01,2024-01-15T11:00:00Z,ZONE_B,OFF_PEAK
`

	var batches []RowBatch
	result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
		batches = append(batches, batch)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, result.RowCount) // Only valid row counted
	assert.Equal(t, 1, result.ErrorCount)

	require.Len(t, batches, 1)
	require.Len(t, batches[0].Errors, 1)
	assert.Equal(t, 2, batches[0].Errors[0].LineNumber)
	assert.Equal(t, "timestamp", batches[0].Errors[0].Column)
	assert.ErrorIs(t, batches[0].Errors[0].Err, ErrInvalidTimestamp)
}

func TestParser_Parse_MissingRequiredField(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	// Empty account_id
	csvData := `account_id,instrument_code,amount,timestamp,grid_zone,tariff_code
,KWH,1234.56,2024-01-15T10:30:00Z,ZONE_A,PEAK
acc-002,KWH,789.01,2024-01-15T11:00:00Z,ZONE_B,OFF_PEAK
`

	var batches []RowBatch
	result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
		batches = append(batches, batch)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, result.RowCount)
	assert.Equal(t, 1, result.ErrorCount)

	require.Len(t, batches, 1)
	require.Len(t, batches[0].Errors, 1)
	assert.Equal(t, 2, batches[0].Errors[0].LineNumber)
	assert.Equal(t, "account_id", batches[0].Errors[0].Column)
}

func TestParser_Parse_SkipEmptyRows(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	csvData := `account_id,instrument_code,amount,timestamp,grid_zone,tariff_code
acc-001,KWH,1234.56,2024-01-15T10:30:00Z,ZONE_A,PEAK
,,,,,
acc-002,KWH,789.01,2024-01-15T11:00:00Z,ZONE_B,OFF_PEAK
`

	config := DefaultParseConfig()
	config.SkipEmptyRows = true

	var batches []RowBatch
	result, err := parser.Parse(context.Background(), strings.NewReader(csvData), config, func(batch RowBatch) error {
		batches = append(batches, batch)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, result.RowCount)
	assert.Equal(t, 0, result.ErrorCount)
}

func TestParser_Parse_Batching(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	// Generate 5 rows
	var csvBuilder strings.Builder
	csvBuilder.WriteString("account_id,instrument_code,amount,timestamp,grid_zone,tariff_code\n")
	for i := 0; i < 5; i++ {
		csvBuilder.WriteString("acc-001,KWH,100.00,2024-01-15T10:30:00Z,ZONE_A,PEAK\n")
	}

	config := DefaultParseConfig()
	config.BatchSize = 2

	var batches []RowBatch
	result, err := parser.Parse(context.Background(), strings.NewReader(csvBuilder.String()), config, func(batch RowBatch) error {
		batches = append(batches, batch)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 5, result.RowCount)
	assert.Len(t, batches, 3) // 2 + 2 + 1

	assert.Len(t, batches[0].Rows, 2)
	assert.Len(t, batches[1].Rows, 2)
	assert.Len(t, batches[2].Rows, 1)
}

func TestParser_Parse_TimestampFormats(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	csvData := `account_id,instrument_code,amount,timestamp,grid_zone,tariff_code
acc-001,KWH,100.00,2024-01-15T10:30:00Z,ZONE_A,PEAK
acc-002,KWH,100.00,2024-01-15T10:30:00,ZONE_A,PEAK
acc-003,KWH,100.00,2024-01-15 10:30:00,ZONE_A,PEAK
acc-004,KWH,100.00,2024-01-15,ZONE_A,PEAK
`

	result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(_ RowBatch) error {
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 4, result.RowCount)
	assert.Equal(t, 0, result.ErrorCount)
}

func TestParser_Parse_ContextCancellation(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	// Generate many rows
	var csvBuilder strings.Builder
	csvBuilder.WriteString("account_id,instrument_code,amount,timestamp,grid_zone,tariff_code\n")
	for i := 0; i < 1000; i++ {
		csvBuilder.WriteString("acc-001,KWH,100.00,2024-01-15T10:30:00Z,ZONE_A,PEAK\n")
	}

	ctx, cancel := context.WithCancel(context.Background())
	batchCount := 0

	config := DefaultParseConfig()
	config.BatchSize = 10

	_, err := parser.Parse(ctx, strings.NewReader(csvBuilder.String()), config, func(_ RowBatch) error {
		batchCount++
		if batchCount >= 5 {
			cancel()
		}
		return nil
	})

	// Should error due to context cancellation
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestParser_Parse_InstrumentWithNoAttributes(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	// USD has empty FungibilityKeyExpression, so no attributes needed
	csvData := `account_id,instrument_code,amount,timestamp
acc-001,USD,1000.00,2024-01-15T10:30:00Z
acc-002,USD,2000.00,2024-01-15T11:00:00Z
`

	var batches []RowBatch
	result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
		batches = append(batches, batch)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, result.RowCount)
	assert.Empty(t, result.Schema.AttributeKeys)
	assert.Empty(t, batches[0].Rows[0].Attributes)
}

func TestParser_Parse_CarbonCreditsComplexAttributes(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	csvData := `account_id,instrument_code,amount,timestamp,vintage_year,certification_type,origin_country
cc-001,CARBON_CREDIT,100,2024-01-15T10:30:00Z,2023,VCS,Brazil
cc-002,CARBON_CREDIT,200,2024-01-15T11:00:00Z,2022,Gold Standard,Kenya
`

	var batches []RowBatch
	result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
		batches = append(batches, batch)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, result.RowCount)
	assert.ElementsMatch(t, []string{"certification_type", "origin_country", "vintage_year"}, result.Schema.AttributeKeys)

	rows := batches[0].Rows
	assert.Equal(t, "2023", rows[0].Attributes["vintage_year"])
	assert.Equal(t, "VCS", rows[0].Attributes["certification_type"])
	assert.Equal(t, "Brazil", rows[0].Attributes["origin_country"])
}

func TestParser_Parse_NormalizedHeaders(t *testing.T) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	// Headers with different casing and spaces
	csvData := `Account ID,Instrument Code,Amount,Timestamp,Grid Zone,Tariff Code
acc-001,KWH,1234.56,2024-01-15T10:30:00Z,ZONE_A,PEAK
`

	var batches []RowBatch
	result, err := parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(batch RowBatch) error {
		batches = append(batches, batch)
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, result.RowCount)
	assert.Equal(t, "ZONE_A", batches[0].Rows[0].Attributes["grid_zone"])
}

func TestRowError_Error(t *testing.T) {
	tests := []struct {
		name     string
		rowErr   RowError
		contains []string
	}{
		{
			name: "with column and value",
			rowErr: RowError{
				LineNumber: 5,
				Column:     "timestamp",
				Value:      "bad-value",
				Err:        errParseTest,
			},
			contains: []string{"line 5", "timestamp", "bad-value", "parse error"},
		},
		{
			name: "without column",
			rowErr: RowError{
				LineNumber: 10,
				Err:        errGeneralTest,
			},
			contains: []string{"line 10", "general error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errStr := tt.rowErr.Error()
			for _, s := range tt.contains {
				assert.Contains(t, errStr, s)
			}
		})
	}
}

func TestParseTimestamp(t *testing.T) {
	formats := DefaultParseConfig().TimestampFormats

	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "RFC3339",
			input:   "2024-01-15T10:30:00Z",
			wantErr: false,
		},
		{
			name:    "RFC3339 with timezone",
			input:   "2024-01-15T10:30:00+05:00",
			wantErr: false,
		},
		{
			name:    "RFC3339Nano",
			input:   "2024-01-15T10:30:00.123456789Z",
			wantErr: false,
		},
		{
			name:    "without timezone",
			input:   "2024-01-15T10:30:00",
			wantErr: false,
		},
		{
			name:    "space separator",
			input:   "2024-01-15 10:30:00",
			wantErr: false,
		},
		{
			name:    "date only",
			input:   "2024-01-15",
			wantErr: false,
		},
		{
			name:    "invalid format",
			input:   "January 15, 2024",
			wantErr: true,
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, err := parseTimestamp(tt.input, formats)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.False(t, ts.IsZero())
			}
		})
	}
}

func TestIsEmptyRow(t *testing.T) {
	tests := []struct {
		name   string
		record []string
		want   bool
	}{
		{
			name:   "all empty",
			record: []string{"", "", ""},
			want:   true,
		},
		{
			name:   "all whitespace",
			record: []string{"  ", "\t", "  \n"},
			want:   true,
		},
		{
			name:   "one value",
			record: []string{"", "value", ""},
			want:   false,
		},
		{
			name:   "empty slice",
			record: []string{},
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEmptyRow(tt.record)
			assert.Equal(t, tt.want, got)
		})
	}
}

func BenchmarkParser_Parse(b *testing.B) {
	registry := newMockRegistry()
	parser := NewParser(registry)

	// Generate 10000 rows
	var csvBuilder strings.Builder
	csvBuilder.WriteString("account_id,instrument_code,amount,timestamp,grid_zone,tariff_code\n")
	for i := 0; i < 10000; i++ {
		csvBuilder.WriteString("acc-001,KWH,100.00,2024-01-15T10:30:00Z,ZONE_A,PEAK\n")
	}
	csvData := csvBuilder.String()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = parser.Parse(context.Background(), strings.NewReader(csvData), DefaultParseConfig(), func(_ RowBatch) error {
			return nil
		})
	}
}
