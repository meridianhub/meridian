package refdata

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInstrumentProperties_ZeroValue(t *testing.T) {
	var props InstrumentProperties
	assert.Equal(t, "", props.Code)
	assert.Equal(t, "", props.Dimension)
	assert.Equal(t, 0, props.Precision)
	assert.Equal(t, "", props.RoundingMode)
}

func TestErrUnknownInstrument_ErrorsIs(t *testing.T) {
	wrapped := errors.Join(ErrUnknownInstrument, errors.New("instrument XYZ not found"))
	assert.True(t, errors.Is(wrapped, ErrUnknownInstrument))
}

func TestErrUnknownInstrument_NotMatched(t *testing.T) {
	other := errors.New("some other error")
	assert.False(t, errors.Is(other, ErrUnknownInstrument))
}

func TestDefaultRoundingMode(t *testing.T) {
	assert.Equal(t, "HALF_EVEN", DefaultRoundingMode)
}

// mockResolver implements InstrumentResolver for testing.
type mockResolver struct {
	instruments map[string]InstrumentProperties
}

func (m *mockResolver) Resolve(_ context.Context, code string) (InstrumentProperties, error) {
	props, ok := m.instruments[code]
	if !ok {
		return InstrumentProperties{}, ErrUnknownInstrument
	}
	return props, nil
}

func TestInstrumentResolver_InterfaceMockable(t *testing.T) {
	resolver := &mockResolver{
		instruments: map[string]InstrumentProperties{
			"USD": {Code: "USD", Dimension: "MONETARY", Precision: 2, RoundingMode: "HALF_EVEN"},
			"KWH": {Code: "KWH", Dimension: "ENERGY", Precision: 4, RoundingMode: "HALF_UP"},
		},
	}

	// Verify interface satisfaction
	var _ InstrumentResolver = resolver

	t.Run("resolve known instrument", func(t *testing.T) {
		props, err := resolver.Resolve(context.Background(), "USD")
		require.NoError(t, err)
		assert.Equal(t, "USD", props.Code)
		assert.Equal(t, "MONETARY", props.Dimension)
		assert.Equal(t, 2, props.Precision)
		assert.Equal(t, "HALF_EVEN", props.RoundingMode)
	})

	t.Run("resolve unknown instrument", func(t *testing.T) {
		_, err := resolver.Resolve(context.Background(), "UNKNOWN")
		assert.ErrorIs(t, err, ErrUnknownInstrument)
	})

	t.Run("different dimensions", func(t *testing.T) {
		props, err := resolver.Resolve(context.Background(), "KWH")
		require.NoError(t, err)
		assert.Equal(t, "ENERGY", props.Dimension)
		assert.Equal(t, 4, props.Precision)
		assert.Equal(t, "HALF_UP", props.RoundingMode)
	})
}
