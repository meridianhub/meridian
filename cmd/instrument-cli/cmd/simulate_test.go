package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestParseAttributes(t *testing.T) {
	tests := []struct {
		name    string
		attrs   []string
		want    map[string]string
		wantErr bool
	}{
		{
			name:  "empty attributes",
			attrs: nil,
			want:  map[string]string{},
		},
		{
			name:  "single attribute",
			attrs: []string{"key=value"},
			want:  map[string]string{"key": "value"},
		},
		{
			name:  "multiple attributes",
			attrs: []string{"key1=value1", "key2=value2"},
			want:  map[string]string{"key1": "value1", "key2": "value2"},
		},
		{
			name:  "attribute with equals in value",
			attrs: []string{"equation=a=b"},
			want:  map[string]string{"equation": "a=b"},
		},
		{
			name:    "invalid attribute format",
			attrs:   []string{"no_equals_sign"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAttributes(tt.attrs)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDimensionToString(t *testing.T) {
	tests := []struct {
		dimension pb.Dimension
		want      string
	}{
		{pb.Dimension_DIMENSION_CURRENCY, "MONETARY"},
		{pb.Dimension_DIMENSION_ENERGY, "ENERGY"},
		{pb.Dimension_DIMENSION_MASS, "MASS"},
		{pb.Dimension_DIMENSION_VOLUME, "VOLUME"},
		{pb.Dimension_DIMENSION_TIME, "TIME"},
		{pb.Dimension_DIMENSION_COMPUTE, "COMPUTE"},
		{pb.Dimension_DIMENSION_CARBON, "CARBON"},
		{pb.Dimension_DIMENSION_DATA, "DATA"},
		{pb.Dimension_DIMENSION_COUNT, "COUNT"},
		{pb.Dimension_DIMENSION_UNSPECIFIED, "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := dimensionToString(tt.dimension)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"exactly_10", 10, "exactly_10"},
		{"this is too long", 10, "this is..."},
		{"", 10, ""},
		{"hello", 3, "..."},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := truncate(tt.input, tt.maxLen)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSortedKeys(t *testing.T) {
	m := map[string]string{
		"zebra": "z",
		"apple": "a",
		"mango": "m",
	}
	got := sortedKeys(m)
	assert.Equal(t, []string{"apple", "mango", "zebra"}, got)
}

func TestSimulate_BasicValidation(t *testing.T) {
	instrDef := &pb.InstrumentDefinition{
		Code:                 "USD",
		Version:              1,
		Dimension:            pb.Dimension_DIMENSION_CURRENCY,
		Precision:            2,
		Status:               pb.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		ValidationExpression: "true",
		CreatedAt:            timestamppb.Now(),
	}

	result := simulate(instrDef, map[string]string{}, "100.00", nil, nil, "")
	assert.True(t, result.ValidationPassed)
	assert.Empty(t, result.ValidationErrors)
	assert.NotEmpty(t, result.BucketID)
}

func TestSimulate_ValidationFailure(t *testing.T) {
	instrDef := &pb.InstrumentDefinition{
		Code:                 "USD",
		Version:              1,
		Dimension:            pb.Dimension_DIMENSION_CURRENCY,
		Precision:            2,
		Status:               pb.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		ValidationExpression: "false",
		CreatedAt:            timestamppb.Now(),
	}

	result := simulate(instrDef, map[string]string{}, "100.00", nil, nil, "")
	assert.False(t, result.ValidationPassed)
	assert.NotEmpty(t, result.ValidationErrors)
}

func TestSimulate_BucketKeyGeneration(t *testing.T) {
	instrDef := &pb.InstrumentDefinition{
		Code:                     "CARBON_CREDIT",
		Version:                  1,
		Dimension:                pb.Dimension_DIMENSION_CARBON,
		Precision:                4,
		Status:                   pb.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		FungibilityKeyExpression: `"CARBON_CREDIT:" + attributes.vintage_year + ":" + attributes.registry`,
		CreatedAt:                timestamppb.Now(),
	}

	attrs := map[string]string{
		"vintage_year": "2024",
		"registry":     "VERRA",
	}

	result := simulate(instrDef, attrs, "50.00", nil, nil, "")
	assert.True(t, result.ValidationPassed)
	assert.Equal(t, "CARBON_CREDIT:2024:VERRA", result.BucketID)
}

func TestSimulate_DefaultBucketKey(t *testing.T) {
	instrDef := &pb.InstrumentDefinition{
		Code:      "USD",
		Version:   1,
		Dimension: pb.Dimension_DIMENSION_CURRENCY,
		Precision: 2,
		Status:    pb.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		CreatedAt: timestamppb.Now(),
	}

	result := simulate(instrDef, map[string]string{}, "100.00", nil, nil, "")
	assert.NotEmpty(t, result.BucketID)
	// Default bucket key should be deterministic for same code+version
	result2 := simulate(instrDef, map[string]string{}, "200.00", nil, nil, "")
	assert.Equal(t, result.BucketID, result2.BucketID)
}

func TestSimulate_PositionPreview(t *testing.T) {
	instrDef := &pb.InstrumentDefinition{
		Code:      "KWH",
		Version:   1,
		Dimension: pb.Dimension_DIMENSION_ENERGY,
		Precision: 6,
		Status:    pb.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		CreatedAt: timestamppb.Now(),
	}

	attrs := map[string]string{"meter_id": "M001"}
	result := simulate(instrDef, attrs, "123.456789", nil, nil, "grid_feed")

	require.NotNil(t, result.PositionPreview)
	assert.Equal(t, "KWH", result.PositionPreview.InstrumentCode)
	assert.Equal(t, 1, result.PositionPreview.Version)
	assert.Equal(t, "123.456789", result.PositionPreview.Amount)
	assert.Equal(t, "ENERGY", result.PositionPreview.Dimension)
	assert.Equal(t, "M001", result.PositionPreview.Attributes["meter_id"])
	assert.Equal(t, "grid_feed", result.PositionPreview.Source)
}

func TestSimulate_CustomErrorMessage(t *testing.T) {
	instrDef := &pb.InstrumentDefinition{
		Code:                   "USD",
		Version:                1,
		Dimension:              pb.Dimension_DIMENSION_CURRENCY,
		Precision:              2,
		Status:                 pb.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE,
		ValidationExpression:   "false",
		ErrorMessageExpression: `"Transaction of " + amount + " failed validation"`,
		CreatedAt:              timestamppb.Now(),
	}

	result := simulate(instrDef, map[string]string{}, "100.00", nil, nil, "")
	assert.False(t, result.ValidationPassed)
	assert.Equal(t, "Transaction of 100.00 failed validation", result.CustomErrorMessage)
}

func TestGenerateDefaultBucketKey(t *testing.T) {
	// Same code+version should generate same key
	key1 := generateDefaultBucketKey("USD", 1)
	key2 := generateDefaultBucketKey("USD", 1)
	assert.Equal(t, key1, key2)

	// Different code should generate different key
	key3 := generateDefaultBucketKey("EUR", 1)
	assert.NotEqual(t, key1, key3)

	// Different version should generate different key
	key4 := generateDefaultBucketKey("USD", 2)
	assert.NotEqual(t, key1, key4)
}
