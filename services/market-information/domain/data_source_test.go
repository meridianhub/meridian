package domain

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDataSource_Success(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		sourceName  string
		description string
		sourceType  SourceType
		trustLevel  int
	}{
		{
			name:        "API source with high trust",
			code:        "BLOOMBERG",
			sourceName:  "Bloomberg Market Data",
			description: "Real-time market data from Bloomberg",
			sourceType:  SourceTypeAPI,
			trustLevel:  90,
		},
		{
			name:        "Manual source with medium trust",
			code:        "MANUAL_ENTRY",
			sourceName:  "Manual Data Entry",
			description: "Data entered by operations team",
			sourceType:  SourceTypeManual,
			trustLevel:  50,
		},
		{
			name:        "Scheduled source with low trust",
			code:        "BATCH_IMPORT",
			sourceName:  "Daily Batch Import",
			description: "Scheduled overnight import from legacy system",
			sourceType:  SourceTypeScheduled,
			trustLevel:  30,
		},
		{
			name:        "Source with zero trust level",
			code:        "UNTRUSTED",
			sourceName:  "Untrusted Source",
			description: "",
			sourceType:  SourceTypeAPI,
			trustLevel:  0,
		},
		{
			name:        "Source with maximum trust level",
			code:        "VERIFIED_OFFICIAL",
			sourceName:  "Official Verified Source",
			description: "Highly trusted official source",
			sourceType:  SourceTypeAPI,
			trustLevel:  100,
		},
		{
			name:        "Source with empty description",
			code:        "SIMPLE",
			sourceName:  "Simple Source",
			description: "",
			sourceType:  SourceTypeManual,
			trustLevel:  50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			beforeCreate := time.Now()

			source, err := NewDataSource(
				tt.code,
				tt.sourceName,
				tt.description,
				tt.sourceType,
				tt.trustLevel,
			)

			require.NoError(t, err)

			// Verify all fields are set correctly
			assert.NotEqual(t, uuid.Nil, source.ID(), "ID should be generated")
			assert.Equal(t, tt.code, source.Code())
			assert.Equal(t, tt.sourceName, source.Name())
			assert.Equal(t, tt.description, source.Description())
			assert.Equal(t, tt.sourceType, source.SourceType())
			assert.Equal(t, tt.trustLevel, source.TrustLevel())

			// Verify initial state
			assert.True(t, source.IsActive(), "initial status should be active")

			// Verify timestamps
			assert.False(t, source.CreatedAt().Before(beforeCreate), "createdAt should be >= beforeCreate")
			assert.False(t, source.UpdatedAt().Before(beforeCreate), "updatedAt should be >= beforeCreate")
			assert.Equal(t, source.CreatedAt(), source.UpdatedAt(), "createdAt and updatedAt should match initially")
		})
	}
}

func TestNewDataSource_ValidationErrors(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		sourceName  string
		description string
		sourceType  SourceType
		trustLevel  int
		expectedErr error
	}{
		{
			name:        "empty code",
			code:        "",
			sourceName:  "Test Source",
			description: "Description",
			sourceType:  SourceTypeAPI,
			trustLevel:  50,
			expectedErr: ErrDataSourceCodeRequired,
		},
		{
			name:        "empty name",
			code:        "TEST",
			sourceName:  "",
			description: "Description",
			sourceType:  SourceTypeAPI,
			trustLevel:  50,
			expectedErr: ErrDataSourceNameRequired,
		},
		{
			name:        "invalid source type",
			code:        "TEST",
			sourceName:  "Test Source",
			description: "Description",
			sourceType:  SourceType("INVALID"),
			trustLevel:  50,
			expectedErr: ErrInvalidSourceType,
		},
		{
			name:        "empty source type",
			code:        "TEST",
			sourceName:  "Test Source",
			description: "Description",
			sourceType:  SourceType(""),
			trustLevel:  50,
			expectedErr: ErrInvalidSourceType,
		},
		{
			name:        "trust level below zero",
			code:        "TEST",
			sourceName:  "Test Source",
			description: "Description",
			sourceType:  SourceTypeAPI,
			trustLevel:  -1,
			expectedErr: ErrInvalidTrustLevel,
		},
		{
			name:        "trust level above 100",
			code:        "TEST",
			sourceName:  "Test Source",
			description: "Description",
			sourceType:  SourceTypeAPI,
			trustLevel:  101,
			expectedErr: ErrInvalidTrustLevel,
		},
		{
			name:        "trust level way above 100",
			code:        "TEST",
			sourceName:  "Test Source",
			description: "Description",
			sourceType:  SourceTypeAPI,
			trustLevel:  1000,
			expectedErr: ErrInvalidTrustLevel,
		},
		{
			name:        "large negative trust level",
			code:        "TEST",
			sourceName:  "Test Source",
			description: "Description",
			sourceType:  SourceTypeAPI,
			trustLevel:  -100,
			expectedErr: ErrInvalidTrustLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, err := NewDataSource(
				tt.code,
				tt.sourceName,
				tt.description,
				tt.sourceType,
				tt.trustLevel,
			)

			require.Error(t, err)
			assert.ErrorIs(t, err, tt.expectedErr)
			assert.Equal(t, DataSource{}, source, "should return zero value on error")
		})
	}
}

func TestNewDataSource_TrustLevelBoundaryValues(t *testing.T) {
	tests := []struct {
		name       string
		trustLevel int
		shouldPass bool
	}{
		{"negative one", -1, false},
		{"zero", 0, true},
		{"one", 1, true},
		{"fifty", 50, true},
		{"ninety nine", 99, true},
		{"one hundred", 100, true},
		{"one hundred one", 101, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, err := NewDataSource(
				"TEST",
				"Test Source",
				"Description",
				SourceTypeAPI,
				tt.trustLevel,
			)

			if tt.shouldPass {
				require.NoError(t, err)
				assert.Equal(t, tt.trustLevel, source.TrustLevel())
			} else {
				require.Error(t, err)
				assert.ErrorIs(t, err, ErrInvalidTrustLevel)
			}
		})
	}
}

func TestSourceType_IsValid(t *testing.T) {
	tests := []struct {
		name       string
		sourceType SourceType
		want       bool
	}{
		{
			name:       "API is valid",
			sourceType: SourceTypeAPI,
			want:       true,
		},
		{
			name:       "MANUAL is valid",
			sourceType: SourceTypeManual,
			want:       true,
		},
		{
			name:       "SCHEDULED is valid",
			sourceType: SourceTypeScheduled,
			want:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.sourceType.IsValid())
		})
	}
}

func TestSourceType_IsValid_Invalid(t *testing.T) {
	tests := []struct {
		name       string
		sourceType SourceType
	}{
		{
			name:       "empty string is invalid",
			sourceType: SourceType(""),
		},
		{
			name:       "unknown type is invalid",
			sourceType: SourceType("UNKNOWN"),
		},
		{
			name:       "lowercase is invalid",
			sourceType: SourceType("api"),
		},
		{
			name:       "mixed case is invalid",
			sourceType: SourceType("Api"),
		},
		{
			name:       "typo is invalid",
			sourceType: SourceType("APII"),
		},
		{
			name:       "extra whitespace is invalid",
			sourceType: SourceType(" API"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.False(t, tt.sourceType.IsValid())
		})
	}
}

func TestSourceType_String(t *testing.T) {
	tests := []struct {
		name       string
		sourceType SourceType
		want       string
	}{
		{
			name:       "API string",
			sourceType: SourceTypeAPI,
			want:       "API",
		},
		{
			name:       "MANUAL string",
			sourceType: SourceTypeManual,
			want:       "MANUAL",
		},
		{
			name:       "SCHEDULED string",
			sourceType: SourceTypeScheduled,
			want:       "SCHEDULED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.sourceType.String())
		})
	}
}

func TestSourceType_Constants(t *testing.T) {
	// Verify the constant values are as expected
	assert.Equal(t, SourceType("API"), SourceTypeAPI)
	assert.Equal(t, SourceType("MANUAL"), SourceTypeManual)
	assert.Equal(t, SourceType("SCHEDULED"), SourceTypeScheduled)
}

func TestSourceType_AllValidTypesCount(t *testing.T) {
	// Test that we have exactly 3 valid source types
	validTypes := []SourceType{
		SourceTypeAPI,
		SourceTypeManual,
		SourceTypeScheduled,
	}

	for _, st := range validTypes {
		assert.True(t, st.IsValid(), "expected %s to be valid", st)
	}

	assert.Len(t, validTypes, 3, "expected exactly 3 valid source types")
}

func TestSourceType_StringOnInvalid(t *testing.T) {
	// Test String() on invalid types returns the underlying string
	invalid := SourceType("INVALID")
	assert.Equal(t, "INVALID", invalid.String())

	empty := SourceType("")
	assert.Equal(t, "", empty.String())
}

func TestDataSourceBuilder_Reconstruction(t *testing.T) {
	// Simulate values from persistence
	id := uuid.New()
	createdAt := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 15, 14, 30, 0, 0, time.UTC)

	// Reconstruct using builder
	source := NewDataSourceBuilder().
		WithID(id).
		WithCode("BLOOMBERG").
		WithName("Bloomberg Market Data").
		WithDescription("Real-time market data from Bloomberg").
		WithSourceType(SourceTypeAPI).
		WithTrustLevel(90).
		WithIsActive(false). // Testing inactive reconstruction
		WithCreatedAt(createdAt).
		WithUpdatedAt(updatedAt).
		Build()

	// Verify all fields were set correctly
	assert.Equal(t, id, source.ID())
	assert.Equal(t, "BLOOMBERG", source.Code())
	assert.Equal(t, "Bloomberg Market Data", source.Name())
	assert.Equal(t, "Real-time market data from Bloomberg", source.Description())
	assert.Equal(t, SourceTypeAPI, source.SourceType())
	assert.Equal(t, 90, source.TrustLevel())
	assert.False(t, source.IsActive())
	assert.Equal(t, createdAt, source.CreatedAt())
	assert.Equal(t, updatedAt, source.UpdatedAt())
}

func TestDataSourceBuilder_MinimalFields(t *testing.T) {
	// Test builder with only essential fields set
	id := uuid.New()
	source := NewDataSourceBuilder().
		WithID(id).
		WithCode("TEST").
		WithName("Test Source").
		Build()

	// Verify essential fields are set
	assert.Equal(t, id, source.ID())
	assert.Equal(t, "TEST", source.Code())
	assert.Equal(t, "Test Source", source.Name())

	// Verify optional fields have zero values
	assert.Empty(t, source.Description())
	assert.Equal(t, SourceType(""), source.SourceType())
	assert.Equal(t, 0, source.TrustLevel())
	assert.False(t, source.IsActive())
	assert.True(t, source.CreatedAt().IsZero())
	assert.True(t, source.UpdatedAt().IsZero())
}

func TestDataSourceBuilder_AllSourceTypes(t *testing.T) {
	tests := []struct {
		name       string
		sourceType SourceType
	}{
		{"API", SourceTypeAPI},
		{"MANUAL", SourceTypeManual},
		{"SCHEDULED", SourceTypeScheduled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := NewDataSourceBuilder().
				WithID(uuid.New()).
				WithCode("TEST").
				WithName("Test").
				WithSourceType(tt.sourceType).
				Build()

			assert.Equal(t, tt.sourceType, source.SourceType())
		})
	}
}

func TestDataSource_UniqueIDsGenerated(t *testing.T) {
	// Create multiple data sources and verify they have unique IDs
	ids := make(map[uuid.UUID]bool)
	for i := 0; i < 100; i++ {
		source, err := NewDataSource(
			"TEST",
			"Test Source",
			"Description",
			SourceTypeAPI,
			50,
		)
		require.NoError(t, err)

		_, exists := ids[source.ID()]
		assert.False(t, exists, "ID should be unique")
		ids[source.ID()] = true
	}
}

func TestDataSource_Immutability(t *testing.T) {
	// Verify that DataSource is a value type and creating a new one
	// doesn't affect the original
	source1, err := NewDataSource(
		"TEST",
		"Test Source",
		"Description",
		SourceTypeAPI,
		50,
	)
	require.NoError(t, err)

	// Create a copy (this is a value copy in Go)
	source2 := source1

	// Verify they have the same values
	assert.Equal(t, source1.ID(), source2.ID())
	assert.Equal(t, source1.Code(), source2.Code())
	assert.Equal(t, source1.Name(), source2.Name())
	assert.Equal(t, source1.TrustLevel(), source2.TrustLevel())
}

func TestNewDataSource_AllSourceTypes(t *testing.T) {
	// Test creating data sources with all valid source types
	tests := []struct {
		name       string
		sourceType SourceType
	}{
		{"API", SourceTypeAPI},
		{"MANUAL", SourceTypeManual},
		{"SCHEDULED", SourceTypeScheduled},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source, err := NewDataSource(
				"CODE_"+tt.name,
				"Test "+tt.name+" Source",
				"Description for "+tt.name,
				tt.sourceType,
				50,
			)

			require.NoError(t, err)
			assert.Equal(t, tt.sourceType, source.SourceType())
			assert.True(t, source.IsActive())
		})
	}
}

func TestDataSource_TrustLevelUsedForPrecedence(t *testing.T) {
	// Demonstrate that trust level can be used for comparison/precedence
	highTrust, err := NewDataSource("HIGH", "High Trust", "", SourceTypeAPI, 90)
	require.NoError(t, err)

	mediumTrust, err := NewDataSource("MEDIUM", "Medium Trust", "", SourceTypeAPI, 50)
	require.NoError(t, err)

	lowTrust, err := NewDataSource("LOW", "Low Trust", "", SourceTypeAPI, 10)
	require.NoError(t, err)

	// Higher trust level means more trusted
	assert.Greater(t, highTrust.TrustLevel(), mediumTrust.TrustLevel())
	assert.Greater(t, mediumTrust.TrustLevel(), lowTrust.TrustLevel())
	assert.Greater(t, highTrust.TrustLevel(), lowTrust.TrustLevel())
}

func TestDataSourceBuilder_ChainedBuilding(t *testing.T) {
	// Test that builder can be built multiple times (creates copies)
	builder := NewDataSourceBuilder().
		WithCode("BASE").
		WithName("Base Source").
		WithSourceType(SourceTypeAPI).
		WithTrustLevel(50).
		WithIsActive(true)

	source1 := builder.WithID(uuid.New()).Build()
	source2 := builder.WithID(uuid.New()).Build()

	// Should have different IDs but same other values
	assert.NotEqual(t, source1.ID(), source2.ID())
	assert.Equal(t, source1.Code(), source2.Code())
	assert.Equal(t, source1.Name(), source2.Name())
}

func TestDataSource_ZeroValueComparison(t *testing.T) {
	// Verify behavior with zero value
	var zeroSource DataSource

	assert.Equal(t, uuid.Nil, zeroSource.ID())
	assert.Equal(t, "", zeroSource.Code())
	assert.Equal(t, "", zeroSource.Name())
	assert.Equal(t, "", zeroSource.Description())
	assert.Equal(t, SourceType(""), zeroSource.SourceType())
	assert.Equal(t, 0, zeroSource.TrustLevel())
	assert.False(t, zeroSource.IsActive())
	assert.True(t, zeroSource.CreatedAt().IsZero())
	assert.True(t, zeroSource.UpdatedAt().IsZero())
}

func TestNewDataSource_TimestampsConsistent(t *testing.T) {
	// Verify that CreatedAt and UpdatedAt are set to the same time on creation
	source, err := NewDataSource(
		"TEST",
		"Test Source",
		"Description",
		SourceTypeAPI,
		50,
	)
	require.NoError(t, err)

	// CreatedAt and UpdatedAt should be equal on creation
	assert.Equal(t, source.CreatedAt(), source.UpdatedAt())

	// Timestamps should be close to now (within 1 second)
	now := time.Now()
	assert.WithinDuration(t, now, source.CreatedAt(), time.Second)
}

func TestDataSourceBuilder_WithIsActive(t *testing.T) {
	tests := []struct {
		name     string
		isActive bool
	}{
		{"active source", true},
		{"inactive source", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := NewDataSourceBuilder().
				WithID(uuid.New()).
				WithCode("TEST").
				WithName("Test").
				WithIsActive(tt.isActive).
				Build()

			assert.Equal(t, tt.isActive, source.IsActive())
		})
	}
}

func TestSourceType_UsedInSwitch(t *testing.T) {
	// Test that source types work correctly in switch statements
	getTypeDescription := func(st SourceType) string {
		switch st {
		case SourceTypeAPI:
			return "external API"
		case SourceTypeManual:
			return "manual entry"
		case SourceTypeScheduled:
			return "scheduled import"
		default:
			return "unknown"
		}
	}

	assert.Equal(t, "external API", getTypeDescription(SourceTypeAPI))
	assert.Equal(t, "manual entry", getTypeDescription(SourceTypeManual))
	assert.Equal(t, "scheduled import", getTypeDescription(SourceTypeScheduled))
	assert.Equal(t, "unknown", getTypeDescription(SourceType("")))
	assert.Equal(t, "unknown", getTypeDescription(SourceType("INVALID")))
}

func TestSourceType_UsedAsMapKey(t *testing.T) {
	// Test that source types can be used as map keys
	descriptions := map[SourceType]string{
		SourceTypeAPI:       "Data from external API",
		SourceTypeManual:    "Manually entered data",
		SourceTypeScheduled: "Data from batch process",
	}

	assert.Equal(t, "Data from external API", descriptions[SourceTypeAPI])
	assert.Equal(t, "Manually entered data", descriptions[SourceTypeManual])
	assert.Equal(t, "Data from batch process", descriptions[SourceTypeScheduled])

	// Invalid key returns zero value
	_, exists := descriptions[SourceType("")]
	assert.False(t, exists)
}
