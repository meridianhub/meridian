package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

func TestMappingToProto_Nil(t *testing.T) {
	t.Parallel()
	assert.Nil(t, mappingToProto(nil))
}

func TestProtoTransformToDomain_Variants(t *testing.T) {
	t.Parallel()

	t.Run("nil input returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, protoTransformToDomain(nil))
	})

	t.Run("enum mapping", func(t *testing.T) {
		t.Parallel()
		in := &mappb.FieldTransform{
			Transform: &mappb.FieldTransform_EnumMapping{
				EnumMapping: &mappb.EnumMapping{
					Values:           map[string]string{"A": "active"},
					Fallback:         "fb",
					OutboundFallback: "ofb",
				},
			},
		}
		out := protoTransformToDomain(in)
		require.NotNil(t, out.EnumMapping)
		assert.Equal(t, map[string]string{"A": "active"}, out.EnumMapping.Values)
		assert.Equal(t, "fb", out.EnumMapping.Fallback)
		assert.Equal(t, "ofb", out.EnumMapping.OutboundFallback)
	})

	t.Run("date format", func(t *testing.T) {
		t.Parallel()
		in := &mappb.FieldTransform{
			Transform: &mappb.FieldTransform_DateFormat{DateFormat: "RFC3339"},
		}
		out := protoTransformToDomain(in)
		assert.Equal(t, "RFC3339", out.DateFormat)
	})

	t.Run("default value", func(t *testing.T) {
		t.Parallel()
		in := &mappb.FieldTransform{
			Transform: &mappb.FieldTransform_DefaultValue{DefaultValue: "N/A"},
		}
		out := protoTransformToDomain(in)
		assert.Equal(t, "N/A", out.DefaultValue)
	})

	t.Run("attribute flatten", func(t *testing.T) {
		t.Parallel()
		in := &mappb.FieldTransform{
			Transform: &mappb.FieldTransform_AttributeFlatten{
				AttributeFlatten: &mappb.AttributeFlatten{
					SourceKeys:  []string{"a", "b"},
					TargetField: "attrs",
				},
			},
		}
		out := protoTransformToDomain(in)
		require.NotNil(t, out.AttributeFlatten)
		assert.Equal(t, []string{"a", "b"}, out.AttributeFlatten.SourceKeys)
		assert.Equal(t, "attrs", out.AttributeFlatten.TargetField)
	})
}

func TestDomainTransformToProto_Variants(t *testing.T) {
	t.Parallel()

	t.Run("nil input returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, domainTransformToProto(nil))
	})

	t.Run("enum mapping", func(t *testing.T) {
		t.Parallel()
		in := &mapping.FieldTransform{
			EnumMapping: &mapping.EnumMapping{
				Values:           map[string]string{"A": "active"},
				Fallback:         "fb",
				OutboundFallback: "ofb",
			},
		}
		out := domainTransformToProto(in)
		em := out.GetEnumMapping()
		require.NotNil(t, em)
		assert.Equal(t, map[string]string{"A": "active"}, em.GetValues())
		assert.Equal(t, "fb", em.GetFallback())
		assert.Equal(t, "ofb", em.GetOutboundFallback())
	})

	t.Run("date format", func(t *testing.T) {
		t.Parallel()
		out := domainTransformToProto(&mapping.FieldTransform{DateFormat: "RFC3339"})
		assert.Equal(t, "RFC3339", out.GetDateFormat())
	})

	t.Run("default value", func(t *testing.T) {
		t.Parallel()
		out := domainTransformToProto(&mapping.FieldTransform{DefaultValue: "N/A"})
		assert.Equal(t, "N/A", out.GetDefaultValue())
	})

	t.Run("attribute flatten", func(t *testing.T) {
		t.Parallel()
		in := &mapping.FieldTransform{
			AttributeFlatten: &mapping.AttributeFlatten{
				SourceKeys:  []string{"a", "b"},
				TargetField: "attrs",
			},
		}
		af := domainTransformToProto(in).GetAttributeFlatten()
		require.NotNil(t, af)
		assert.Equal(t, []string{"a", "b"}, af.GetSourceKeys())
		assert.Equal(t, "attrs", af.GetTargetField())
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
