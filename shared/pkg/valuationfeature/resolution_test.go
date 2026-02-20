package valuationfeature

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reference-data/accounttype"
	"github.com/meridianhub/meridian/services/reference-data/registry"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveConversionMethod_ExplicitTemplate(t *testing.T) {
	methodID := uuid.New()
	convMethodID := uuid.New()
	convMethodVersion := 3

	productType := &accounttype.Definition{
		ID:                             uuid.New(),
		Code:                           "TEST_TYPE",
		DefaultConversionMethodID:      &convMethodID,
		DefaultConversionMethodVersion: &convMethodVersion,
		ValuationMethods: []accounttype.ValuationMethodTemplate{
			{
				ID:                     uuid.New(),
				InputInstrument:        "TONNE_CO2E",
				ValuationMethodID:      methodID,
				ValuationMethodVersion: 1,
				Status:                 accounttype.StatusActive,
			},
		},
	}

	inputInstrument := &registry.InstrumentDefinition{
		Code:      "TONNE_CO2E",
		Dimension: registry.DimensionMass,
	}
	accountInstrument := &registry.InstrumentDefinition{
		Code:      "GBP",
		Dimension: registry.DimensionMonetary,
	}

	result, err := ResolveConversionMethod(productType, inputInstrument, accountInstrument)

	require.NoError(t, err)
	assert.Equal(t, methodID, result.ValuationMethodID)
	assert.Equal(t, 1, result.ValuationMethodVersion)
}

func TestResolveConversionMethod_SameDimensionFallback(t *testing.T) {
	convMethodID := uuid.New()
	convMethodVersion := 2

	productType := &accounttype.Definition{
		ID:                             uuid.New(),
		Code:                           "TEST_TYPE",
		DefaultConversionMethodID:      &convMethodID,
		DefaultConversionMethodVersion: &convMethodVersion,
		ValuationMethods:               []accounttype.ValuationMethodTemplate{},
	}

	inputInstrument := &registry.InstrumentDefinition{
		Code:      "EUR",
		Dimension: registry.DimensionMonetary,
	}
	accountInstrument := &registry.InstrumentDefinition{
		Code:      "GBP",
		Dimension: registry.DimensionMonetary,
	}

	result, err := ResolveConversionMethod(productType, inputInstrument, accountInstrument)

	require.NoError(t, err)
	assert.Equal(t, convMethodID, result.ValuationMethodID)
	assert.Equal(t, convMethodVersion, result.ValuationMethodVersion)
}

func TestResolveConversionMethod_ExplicitTemplateBeforeSameDimension(t *testing.T) {
	explicitMethodID := uuid.New()
	defaultMethodID := uuid.New()
	defaultVersion := 1

	productType := &accounttype.Definition{
		ID:                             uuid.New(),
		Code:                           "TEST_TYPE",
		DefaultConversionMethodID:      &defaultMethodID,
		DefaultConversionMethodVersion: &defaultVersion,
		ValuationMethods: []accounttype.ValuationMethodTemplate{
			{
				ID:                     uuid.New(),
				InputInstrument:        "EUR",
				ValuationMethodID:      explicitMethodID,
				ValuationMethodVersion: 5,
				Status:                 accounttype.StatusActive,
			},
		},
	}

	// Both instruments share the same dimension, but an explicit template exists.
	// The explicit template should take priority.
	inputInstrument := &registry.InstrumentDefinition{
		Code:      "EUR",
		Dimension: registry.DimensionMonetary,
	}
	accountInstrument := &registry.InstrumentDefinition{
		Code:      "GBP",
		Dimension: registry.DimensionMonetary,
	}

	result, err := ResolveConversionMethod(productType, inputInstrument, accountInstrument)

	require.NoError(t, err)
	assert.Equal(t, explicitMethodID, result.ValuationMethodID, "explicit template takes priority over same-dimension default")
	assert.Equal(t, 5, result.ValuationMethodVersion)
}

func TestResolveConversionMethod_CrossDimensionNoTemplate(t *testing.T) {
	convMethodID := uuid.New()
	convMethodVersion := 1

	productType := &accounttype.Definition{
		ID:                             uuid.New(),
		Code:                           "TEST_TYPE",
		DefaultConversionMethodID:      &convMethodID,
		DefaultConversionMethodVersion: &convMethodVersion,
		ValuationMethods:               []accounttype.ValuationMethodTemplate{},
	}

	inputInstrument := &registry.InstrumentDefinition{
		Code:      "KWH",
		Dimension: registry.DimensionEnergy,
	}
	accountInstrument := &registry.InstrumentDefinition{
		Code:      "GBP",
		Dimension: registry.DimensionMonetary,
	}

	_, err := ResolveConversionMethod(productType, inputInstrument, accountInstrument)

	assert.ErrorIs(t, err, ErrNoConversionAvailable)
}

func TestResolveConversionMethod_SameDimensionNoDefault(t *testing.T) {
	productType := &accounttype.Definition{
		ID:                             uuid.New(),
		Code:                           "TEST_TYPE",
		DefaultConversionMethodID:      nil,
		DefaultConversionMethodVersion: nil,
		ValuationMethods:               []accounttype.ValuationMethodTemplate{},
	}

	inputInstrument := &registry.InstrumentDefinition{
		Code:      "EUR",
		Dimension: registry.DimensionMonetary,
	}
	accountInstrument := &registry.InstrumentDefinition{
		Code:      "GBP",
		Dimension: registry.DimensionMonetary,
	}

	_, err := ResolveConversionMethod(productType, inputInstrument, accountInstrument)

	assert.ErrorIs(t, err, ErrNoConversionAvailable)
}

func TestResolveConversionMethod_DeprecatedTemplateSkipped(t *testing.T) {
	convMethodID := uuid.New()
	convMethodVersion := 1

	productType := &accounttype.Definition{
		ID:                             uuid.New(),
		Code:                           "TEST_TYPE",
		DefaultConversionMethodID:      &convMethodID,
		DefaultConversionMethodVersion: &convMethodVersion,
		ValuationMethods: []accounttype.ValuationMethodTemplate{
			{
				ID:                     uuid.New(),
				InputInstrument:        "TONNE_CO2E",
				ValuationMethodID:      uuid.New(),
				ValuationMethodVersion: 1,
				Status:                 accounttype.StatusDeprecated, // should not match
			},
		},
	}

	inputInstrument := &registry.InstrumentDefinition{
		Code:      "TONNE_CO2E",
		Dimension: registry.DimensionMass,
	}
	accountInstrument := &registry.InstrumentDefinition{
		Code:      "GBP",
		Dimension: registry.DimensionMonetary,
	}

	// Deprecated template does not match, no same-dimension fallback possible (different dimensions),
	// no default method for cross-dimension → ErrNoConversionAvailable.
	// (DefaultConversionMethodID only helps for same-dimension; here dimensions differ.)
	_, err := ResolveConversionMethod(productType, inputInstrument, accountInstrument)

	assert.ErrorIs(t, err, ErrNoConversionAvailable)
}
