package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewObservationContext_WithAttributes(t *testing.T) {
	attrs := map[string]string{"base_code": "USD", "quote_code": "EUR"}
	ctx := NewObservationContext(attrs)

	assert.Equal(t, attrs, ctx.Attributes)
	assert.False(t, ctx.IsEmpty())
}

func TestNewObservationContext_NilAttributes(t *testing.T) {
	ctx := NewObservationContext(nil)

	assert.NotNil(t, ctx.Attributes)
	assert.Empty(t, ctx.Attributes)
	assert.True(t, ctx.IsEmpty())
}

func TestNewObservationContext_EmptyAttributes(t *testing.T) {
	ctx := NewObservationContext(map[string]string{})

	assert.NotNil(t, ctx.Attributes)
	assert.Empty(t, ctx.Attributes)
	assert.True(t, ctx.IsEmpty())
}

func TestObservationContext_IsEmpty(t *testing.T) {
	tests := []struct {
		name    string
		ctx     ObservationContext
		isEmpty bool
	}{
		{
			name:    "zero value is empty",
			ctx:     ObservationContext{},
			isEmpty: true,
		},
		{
			name:    "only empty attributes is empty",
			ctx:     ObservationContext{Attributes: map[string]string{}},
			isEmpty: true,
		},
		{
			name:    "with attributes is not empty",
			ctx:     ObservationContext{Attributes: map[string]string{"k": "v"}},
			isEmpty: false,
		},
		{
			name:    "with source system is not empty",
			ctx:     ObservationContext{SourceSystem: "bloomberg"},
			isEmpty: false,
		},
		{
			name:    "with collection method is not empty",
			ctx:     ObservationContext{CollectionMethod: "api-poll"},
			isEmpty: false,
		},
		{
			name:    "with unit is not empty",
			ctx:     ObservationContext{Unit: "USD/oz"},
			isEmpty: false,
		},
		{
			name:    "with notes is not empty",
			ctx:     ObservationContext{Notes: "manual correction"},
			isEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.isEmpty, tt.ctx.IsEmpty())
		})
	}
}

func TestObservationContext_OnObservation(t *testing.T) {
	attrs := map[string]string{"base_code": "USD", "quote_code": "EUR"}
	ctx := NewObservationContext(attrs)
	ctx.SourceSystem = "bloomberg"

	obs := NewMarketPriceObservationBuilder().
		WithObservationContext(ctx).
		Build()

	result := obs.ObservationContext()
	assert.Equal(t, attrs, result.Attributes)
	assert.Equal(t, "bloomberg", result.SourceSystem)
}
