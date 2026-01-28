package saga

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIsPhysicsInstrument tests the physics instrument detection logic.
func TestIsPhysicsInstrument(t *testing.T) {
	tests := []struct {
		name       string
		instrument string
		expected   bool
	}{
		{"KWH is physics", "KWH", true},
		{"GAS is physics", "GAS", true},
		{"USD is not physics", "USD", false},
		{"NZD is not physics", "NZD", false},
		{"EUR is not physics", "EUR", false},
		{"empty string is not physics", "", false},
		{"lowercase kwh is not physics", "kwh", false}, // Strict match
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsPhysicsInstrument(tt.instrument)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestCheckConservationRule tests conservation rule enforcement.
func TestCheckConservationRule(t *testing.T) {
	t.Run("allows settlement saga with USD trigger to produce USD", func(t *testing.T) {
		metadata := &HandlerMetadata{
			Category:            HandlerCategorySettlement,
			ProducesInstruments: []string{"USD"},
		}
		ctx := &StarlarkContext{
			Context:           context.Background(),
			SagaExecutionID:   uuid.New(),
			TriggerInstrument: "USD",
			Logger:            slog.Default(),
		}

		err := CheckConservationRule(ctx, metadata, "test.handler")
		require.NoError(t, err)
	})

	t.Run("allows settlement saga with KWH trigger to produce USD", func(t *testing.T) {
		metadata := &HandlerMetadata{
			Category:            HandlerCategorySettlement,
			ProducesInstruments: []string{"USD"},
		}
		ctx := &StarlarkContext{
			Context:           context.Background(),
			SagaExecutionID:   uuid.New(),
			TriggerInstrument: "KWH",
			Logger:            slog.Default(),
		}

		err := CheckConservationRule(ctx, metadata, "test.handler")
		require.NoError(t, err)
	})

	t.Run("blocks settlement saga with KWH trigger from producing KWH", func(t *testing.T) {
		metadata := &HandlerMetadata{
			Category:            HandlerCategorySettlement,
			ProducesInstruments: []string{"KWH"},
		}
		ctx := &StarlarkContext{
			Context:           context.Background(),
			SagaExecutionID:   uuid.New(),
			TriggerInstrument: "KWH",
			Logger:            slog.Default(),
		}

		err := CheckConservationRule(ctx, metadata, "test.handler")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrConservationViolation)
		assert.Contains(t, err.Error(), "test.handler")
		assert.Contains(t, err.Error(), "KWH")
	})

	t.Run("blocks settlement saga with GAS trigger from producing GAS", func(t *testing.T) {
		metadata := &HandlerMetadata{
			Category:            HandlerCategorySettlement,
			ProducesInstruments: []string{"GAS"},
		}
		ctx := &StarlarkContext{
			Context:           context.Background(),
			SagaExecutionID:   uuid.New(),
			TriggerInstrument: "GAS",
			Logger:            slog.Default(),
		}

		err := CheckConservationRule(ctx, metadata, "test.handler")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrConservationViolation)
	})

	t.Run("blocks settlement saga with KWH trigger from producing both USD and KWH", func(t *testing.T) {
		metadata := &HandlerMetadata{
			Category:            HandlerCategorySettlement,
			ProducesInstruments: []string{"USD", "KWH"},
		}
		ctx := &StarlarkContext{
			Context:           context.Background(),
			SagaExecutionID:   uuid.New(),
			TriggerInstrument: "KWH",
			Logger:            slog.Default(),
		}

		err := CheckConservationRule(ctx, metadata, "test.handler")
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrConservationViolation)
	})

	t.Run("allows ingestion handlers to produce KWH regardless of trigger", func(t *testing.T) {
		metadata := &HandlerMetadata{
			Category:            HandlerCategoryIngestion,
			ProducesInstruments: []string{"KWH"},
		}
		ctx := &StarlarkContext{
			Context:           context.Background(),
			SagaExecutionID:   uuid.New(),
			TriggerInstrument: "KWH",
			Logger:            slog.Default(),
		}

		err := CheckConservationRule(ctx, metadata, "test.handler")
		require.NoError(t, err)
	})

	t.Run("allows valuation handlers regardless of instruments", func(t *testing.T) {
		metadata := &HandlerMetadata{
			Category:            HandlerCategoryValuation,
			ProducesInstruments: []string{"KWH", "USD"},
		}
		ctx := &StarlarkContext{
			Context:           context.Background(),
			SagaExecutionID:   uuid.New(),
			TriggerInstrument: "KWH",
			Logger:            slog.Default(),
		}

		err := CheckConservationRule(ctx, metadata, "test.handler")
		require.NoError(t, err)
	})

	t.Run("allows handlers without metadata - backward compatibility", func(t *testing.T) {
		ctx := &StarlarkContext{
			Context:           context.Background(),
			SagaExecutionID:   uuid.New(),
			TriggerInstrument: "KWH",
			Logger:            slog.Default(),
		}

		err := CheckConservationRule(ctx, nil, "test.handler")
		require.NoError(t, err)
	})

	t.Run("allows empty trigger instrument", func(t *testing.T) {
		metadata := &HandlerMetadata{
			Category:            HandlerCategorySettlement,
			ProducesInstruments: []string{"KWH"},
		}
		ctx := &StarlarkContext{
			Context:           context.Background(),
			SagaExecutionID:   uuid.New(),
			TriggerInstrument: "",
			Logger:            slog.Default(),
		}

		err := CheckConservationRule(ctx, metadata, "test.handler")
		require.NoError(t, err)
	})

	t.Run("allows handlers that don't produce instruments", func(t *testing.T) {
		metadata := &HandlerMetadata{
			Category:            HandlerCategorySettlement,
			ProducesInstruments: []string{},
		}
		ctx := &StarlarkContext{
			Context:           context.Background(),
			SagaExecutionID:   uuid.New(),
			TriggerInstrument: "KWH",
			Logger:            slog.Default(),
		}

		err := CheckConservationRule(ctx, metadata, "test.handler")
		require.NoError(t, err)
	})
}
