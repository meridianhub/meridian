package persistence

import (
	"testing"

	"github.com/meridianhub/meridian/services/market-information/domain"
	"github.com/stretchr/testify/assert"
)

func TestMarshalObservationContext_Empty(t *testing.T) {
	ctx := domain.ObservationContext{}
	data := marshalObservationContext(ctx)
	assert.Equal(t, []byte("{}"), data)
}

func TestMarshalObservationContext_WithAttributes(t *testing.T) {
	ctx := domain.NewObservationContext(map[string]string{
		"base_code":  "USD",
		"quote_code": "EUR",
	})
	data := marshalObservationContext(ctx)

	// Unmarshal back and verify round-trip
	result := unmarshalObservationContext(data)
	assert.Equal(t, "USD", result.Attributes["base_code"])
	assert.Equal(t, "EUR", result.Attributes["quote_code"])
}

func TestMarshalObservationContext_WithAllFields(t *testing.T) {
	ctx := domain.ObservationContext{
		Attributes:       map[string]string{"tenor": "1M"},
		SourceSystem:     "bloomberg",
		CollectionMethod: "api-poll",
		Unit:             "USD/oz",
		Notes:            "manual correction",
	}
	data := marshalObservationContext(ctx)

	result := unmarshalObservationContext(data)
	assert.Equal(t, "1M", result.Attributes["tenor"])
	assert.Equal(t, "bloomberg", result.SourceSystem)
	assert.Equal(t, "api-poll", result.CollectionMethod)
	assert.Equal(t, "USD/oz", result.Unit)
	assert.Equal(t, "manual correction", result.Notes)
}

func TestUnmarshalObservationContext_Nil(t *testing.T) {
	result := unmarshalObservationContext(nil)
	assert.True(t, result.IsEmpty())
}

func TestUnmarshalObservationContext_EmptyBytes(t *testing.T) {
	result := unmarshalObservationContext([]byte{})
	assert.True(t, result.IsEmpty())
}

func TestUnmarshalObservationContext_EmptyJSON(t *testing.T) {
	result := unmarshalObservationContext([]byte("{}"))
	assert.True(t, result.IsEmpty())
}

func TestUnmarshalObservationContext_InvalidJSON(t *testing.T) {
	result := unmarshalObservationContext([]byte("not json"))
	assert.True(t, result.IsEmpty())
}

func TestUnmarshalObservationContext_LegacyEmptyObject(t *testing.T) {
	// Existing records store "{}" - must deserialize gracefully
	result := unmarshalObservationContext([]byte("{}"))
	assert.True(t, result.IsEmpty())
	assert.Nil(t, result.Attributes) // omitempty means no "attributes" key in "{}"
}

func TestObservationContextRoundTrip_ViaMapper(t *testing.T) {
	ctx := domain.ObservationContext{
		Attributes:       map[string]string{"base_code": "GBP", "quote_code": "USD"},
		SourceSystem:     "internal-engine",
		CollectionMethod: "streaming-feed",
	}

	// Build a domain observation with context
	obs := domain.NewMarketPriceObservationBuilder().
		WithObservationContext(ctx).
		Build()

	// Assert the getter returns what we set
	assert.Equal(t, ctx, obs.ObservationContext())
}
