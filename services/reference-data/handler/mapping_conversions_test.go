package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"

	mappb "github.com/meridianhub/meridian/api/proto/meridian/mapping/v1"
	"github.com/meridianhub/meridian/services/reference-data/mapping"
)

func TestProtoStatusToDomainMapping_AllStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		proto    mappb.MappingStatus
		expected mapping.Status
	}{
		{
			name:     "UNSPECIFIED maps to empty",
			proto:    mappb.MappingStatus_MAPPING_STATUS_UNSPECIFIED,
			expected: "",
		},
		{
			name:     "DRAFT maps to StatusDraft",
			proto:    mappb.MappingStatus_MAPPING_STATUS_DRAFT,
			expected: mapping.StatusDraft,
		},
		{
			name:     "ACTIVE maps to StatusActive",
			proto:    mappb.MappingStatus_MAPPING_STATUS_ACTIVE,
			expected: mapping.StatusActive,
		},
		{
			name:     "DEPRECATED maps to StatusDeprecated",
			proto:    mappb.MappingStatus_MAPPING_STATUS_DEPRECATED,
			expected: mapping.StatusDeprecated,
		},
		{
			name:     "unknown value maps to empty",
			proto:    mappb.MappingStatus(999),
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, protoStatusToDomainMapping(tc.proto))
		})
	}
}

func TestDomainStatusToProtoMapping_AllStatuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		domain   mapping.Status
		expected mappb.MappingStatus
	}{
		{
			name:     "StatusDraft maps to DRAFT",
			domain:   mapping.StatusDraft,
			expected: mappb.MappingStatus_MAPPING_STATUS_DRAFT,
		},
		{
			name:     "StatusActive maps to ACTIVE",
			domain:   mapping.StatusActive,
			expected: mappb.MappingStatus_MAPPING_STATUS_ACTIVE,
		},
		{
			name:     "StatusDeprecated maps to DEPRECATED",
			domain:   mapping.StatusDeprecated,
			expected: mappb.MappingStatus_MAPPING_STATUS_DEPRECATED,
		},
		{
			name:     "unknown domain status maps to UNSPECIFIED",
			domain:   mapping.Status("UNKNOWN"),
			expected: mappb.MappingStatus_MAPPING_STATUS_UNSPECIFIED,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, domainStatusToProtoMapping(tc.domain))
		})
	}
}

func TestProtoComputedFieldsToDomain(t *testing.T) {
	t.Parallel()

	t.Run("nil input returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, protoComputedFieldsToDomain(nil))
	})

	t.Run("empty slice returns empty slice", func(t *testing.T) {
		t.Parallel()
		result := protoComputedFieldsToDomain([]*mappb.ComputedField{})
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("populated fields convert correctly", func(t *testing.T) {
		t.Parallel()
		input := []*mappb.ComputedField{
			{TargetPath: "amount", CelExpression: "parse_decimal(raw_amount)"},
			{TargetPath: "status", CelExpression: `"ACTIVE"`},
		}

		result := protoComputedFieldsToDomain(input)
		assert.Len(t, result, 2)
		assert.Equal(t, "amount", result[0].TargetPath)
		assert.Equal(t, "parse_decimal(raw_amount)", result[0].CELExpression)
		assert.Equal(t, "status", result[1].TargetPath)
		assert.Equal(t, `"ACTIVE"`, result[1].CELExpression)
	})
}

func TestComputedFieldsToProto(t *testing.T) {
	t.Parallel()

	t.Run("nil input returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, computedFieldsToProto(nil))
	})

	t.Run("empty slice returns empty slice", func(t *testing.T) {
		t.Parallel()
		result := computedFieldsToProto([]mapping.ComputedField{})
		assert.NotNil(t, result)
		assert.Empty(t, result)
	})

	t.Run("populated fields convert correctly", func(t *testing.T) {
		t.Parallel()
		input := []mapping.ComputedField{
			{TargetPath: "total", CELExpression: "quantity * price"},
			{TargetPath: "timestamp", CELExpression: "now()"},
		}

		result := computedFieldsToProto(input)
		assert.Len(t, result, 2)
		assert.Equal(t, "total", result[0].TargetPath)
		assert.Equal(t, "quantity * price", result[0].CelExpression)
		assert.Equal(t, "timestamp", result[1].TargetPath)
		assert.Equal(t, "now()", result[1].CelExpression)
	})
}
