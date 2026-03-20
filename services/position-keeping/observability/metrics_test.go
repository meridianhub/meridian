package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetNoopIdempotencyActive(t *testing.T) {
	t.Run("set active", func(t *testing.T) {
		require.NotPanics(t, func() {
			SetNoopIdempotencyActive(true)
		})
	})
	t.Run("set inactive", func(t *testing.T) {
		require.NotPanics(t, func() {
			SetNoopIdempotencyActive(false)
		})
	})
}

func TestRecordServiceDegradation(t *testing.T) {
	require.NotPanics(t, func() {
		RecordServiceDegradation(ComponentIdempotency, DegradationReasonStartupFallback)
	})
}

func TestComponentConstants(t *testing.T) {
	assert.Equal(t, "idempotency", ComponentIdempotency)
}

func TestDegradationReasonConstants(t *testing.T) {
	assert.Equal(t, "startup_fallback", DegradationReasonStartupFallback)
}
