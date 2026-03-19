package client

import (
	"testing"

	"github.com/stretchr/testify/assert"

	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/reference-data/registry"
)

func TestDimensionToString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    referencedatav1.Dimension
		expected string
	}{
		{"UNSPECIFIED", referencedatav1.Dimension_DIMENSION_UNSPECIFIED, "UNSPECIFIED"},
		{"CURRENCY", referencedatav1.Dimension_DIMENSION_CURRENCY, "CURRENCY"},
		{"ENERGY", referencedatav1.Dimension_DIMENSION_ENERGY, "ENERGY"},
		{"MASS", referencedatav1.Dimension_DIMENSION_MASS, "MASS"},
		{"VOLUME", referencedatav1.Dimension_DIMENSION_VOLUME, "VOLUME"},
		{"TIME", referencedatav1.Dimension_DIMENSION_TIME, "TIME"},
		{"COMPUTE", referencedatav1.Dimension_DIMENSION_COMPUTE, "COMPUTE"},
		{"CARBON", referencedatav1.Dimension_DIMENSION_CARBON, "CARBON"},
		{"DATA", referencedatav1.Dimension_DIMENSION_DATA, "DATA"},
		{"COUNT", referencedatav1.Dimension_DIMENSION_COUNT, "COUNT"},
		{"unknown value defaults to UNSPECIFIED", referencedatav1.Dimension(999), "UNSPECIFIED"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, dimensionToString(tc.input))
		})
	}
}

func TestStatusFromProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    referencedatav1.InstrumentStatus
		expected registry.Status
	}{
		{"DRAFT", referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DRAFT, registry.StatusDraft},
		{"ACTIVE", referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_ACTIVE, registry.StatusActive},
		{"DEPRECATED", referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_DEPRECATED, registry.StatusDeprecated},
		{"UNSPECIFIED", referencedatav1.InstrumentStatus_INSTRUMENT_STATUS_UNSPECIFIED, ""},
		{"unknown value defaults to empty", referencedatav1.InstrumentStatus(999), ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, statusFromProto(tc.input))
		})
	}
}

func TestDimensionFromProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    referencedatav1.Dimension
		expected registry.Dimension
	}{
		{"CURRENCY maps to DimensionMonetary", referencedatav1.Dimension_DIMENSION_CURRENCY, registry.DimensionMonetary},
		{"ENERGY maps to DimensionEnergy", referencedatav1.Dimension_DIMENSION_ENERGY, registry.DimensionEnergy},
		{"MASS maps to DimensionMass", referencedatav1.Dimension_DIMENSION_MASS, registry.DimensionMass},
		{"VOLUME maps to DimensionVolume", referencedatav1.Dimension_DIMENSION_VOLUME, registry.DimensionVolume},
		{"TIME maps to DimensionTime", referencedatav1.Dimension_DIMENSION_TIME, registry.DimensionTime},
		{"COMPUTE maps to DimensionCompute", referencedatav1.Dimension_DIMENSION_COMPUTE, registry.DimensionCompute},
		{"COUNT maps to DimensionQuantity", referencedatav1.Dimension_DIMENSION_COUNT, registry.DimensionQuantity},
		{"UNSPECIFIED maps to empty", referencedatav1.Dimension_DIMENSION_UNSPECIFIED, ""},
		{"CARBON maps to empty", referencedatav1.Dimension_DIMENSION_CARBON, ""},
		{"DATA maps to empty", referencedatav1.Dimension_DIMENSION_DATA, ""},
		{"unknown value maps to empty", referencedatav1.Dimension(999), ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.expected, dimensionFromProto(tc.input))
		})
	}
}
