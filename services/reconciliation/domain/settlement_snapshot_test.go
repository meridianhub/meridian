package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSettlementSnapshot(t *testing.T) {
	runID := uuid.New()

	tests := []struct {
		name           string
		runID          uuid.UUID
		accountID      string
		instrumentCode string
		expected       decimal.Decimal
		actual         decimal.Decimal
		sourceSystem   string
		wantErr        error
	}{
		{
			name:           "valid snapshot with no variance",
			runID:          runID,
			accountID:      "ACC-001",
			instrumentCode: "GBP",
			expected:       decimal.NewFromFloat(1000.00),
			actual:         decimal.NewFromFloat(1000.00),
			sourceSystem:   "position-keeping",
			wantErr:        nil,
		},
		{
			name:           "valid snapshot with variance",
			runID:          runID,
			accountID:      "ACC-001",
			instrumentCode: "GBP",
			expected:       decimal.NewFromFloat(1000.00),
			actual:         decimal.NewFromFloat(995.50),
			sourceSystem:   "position-keeping",
			wantErr:        nil,
		},
		{
			name:           "empty account ID",
			runID:          runID,
			accountID:      "",
			instrumentCode: "GBP",
			expected:       decimal.NewFromFloat(1000.00),
			actual:         decimal.NewFromFloat(1000.00),
			sourceSystem:   "position-keeping",
			wantErr:        domain.ErrEmptyAccountID,
		},
		{
			name:           "empty instrument code",
			runID:          runID,
			accountID:      "ACC-001",
			instrumentCode: "",
			expected:       decimal.NewFromFloat(1000.00),
			actual:         decimal.NewFromFloat(1000.00),
			sourceSystem:   "position-keeping",
			wantErr:        domain.ErrEmptyInstrumentCode,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snap, err := domain.NewSettlementSnapshot(
				tt.runID, tt.accountID, tt.instrumentCode,
				tt.expected, tt.actual, tt.sourceSystem, nil,
			)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, snap)
			} else {
				require.NoError(t, err)
				require.NotNil(t, snap)

				assert.NotEqual(t, uuid.Nil, snap.SnapshotID)
				assert.Equal(t, tt.runID, snap.RunID)
				assert.Equal(t, tt.accountID, snap.AccountID)
				assert.Equal(t, tt.instrumentCode, snap.InstrumentCode)
				assert.True(t, tt.expected.Equal(snap.ExpectedBalance))
				assert.True(t, tt.actual.Equal(snap.ActualBalance))

				expectedVariance := tt.actual.Sub(tt.expected)
				assert.True(t, expectedVariance.Equal(snap.VarianceAmount))
			}
		})
	}
}

func TestSettlementSnapshot_HasVariance(t *testing.T) {
	runID := uuid.New()

	t.Run("no variance when balances match", func(t *testing.T) {
		snap, err := domain.NewSettlementSnapshot(
			runID, "ACC-001", "GBP",
			decimal.NewFromFloat(1000.00), decimal.NewFromFloat(1000.00),
			"position-keeping", nil,
		)
		require.NoError(t, err)
		assert.False(t, snap.HasVariance())
	})

	t.Run("has variance when balances differ", func(t *testing.T) {
		snap, err := domain.NewSettlementSnapshot(
			runID, "ACC-001", "GBP",
			decimal.NewFromFloat(1000.00), decimal.NewFromFloat(999.99),
			"position-keeping", nil,
		)
		require.NoError(t, err)
		assert.True(t, snap.HasVariance())
	})
}
