package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewVariance(t *testing.T) {
	runID := uuid.New()
	snapshotID := uuid.New()

	tests := []struct {
		name           string
		accountID      string
		instrumentCode string
		expected       decimal.Decimal
		actual         decimal.Decimal
		reason         domain.VarianceReason
		wantErr        error
	}{
		{
			name:           "valid amount mismatch",
			accountID:      "ACC-001",
			instrumentCode: "GBP",
			expected:       decimal.NewFromFloat(1000.00),
			actual:         decimal.NewFromFloat(995.50),
			reason:         domain.VarianceReasonAmountMismatch,
			wantErr:        nil,
		},
		{
			name:           "valid missing entry",
			accountID:      "ACC-002",
			instrumentCode: "KWH",
			expected:       decimal.NewFromFloat(500.00),
			actual:         decimal.Zero,
			reason:         domain.VarianceReasonMissingEntry,
			wantErr:        nil,
		},
		{
			name:           "empty account ID",
			accountID:      "",
			instrumentCode: "GBP",
			expected:       decimal.NewFromFloat(1000.00),
			actual:         decimal.NewFromFloat(995.50),
			reason:         domain.VarianceReasonAmountMismatch,
			wantErr:        domain.ErrEmptyAccountID,
		},
		{
			name:           "empty instrument code",
			accountID:      "ACC-001",
			instrumentCode: "",
			expected:       decimal.NewFromFloat(1000.00),
			actual:         decimal.NewFromFloat(995.50),
			reason:         domain.VarianceReasonAmountMismatch,
			wantErr:        domain.ErrEmptyInstrumentCode,
		},
		{
			name:           "invalid reason",
			accountID:      "ACC-001",
			instrumentCode: "GBP",
			expected:       decimal.NewFromFloat(1000.00),
			actual:         decimal.NewFromFloat(995.50),
			reason:         domain.VarianceReason("INVALID"),
			wantErr:        domain.ErrEmptyVarianceReason,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := domain.NewVariance(
				runID, snapshotID, tt.accountID, tt.instrumentCode,
				tt.expected, tt.actual, tt.reason,
			)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, v)
			} else {
				require.NoError(t, err)
				require.NotNil(t, v)

				assert.NotEqual(t, uuid.Nil, v.VarianceID)
				assert.Equal(t, runID, v.RunID)
				assert.Equal(t, snapshotID, v.SnapshotID)
				assert.Equal(t, domain.VarianceStatusDetected, v.Status)

				expectedVariance := tt.actual.Sub(tt.expected)
				assert.True(t, expectedVariance.Equal(v.VarianceAmount))
			}
		})
	}
}

func TestVariance_Lifecycle(t *testing.T) {
	v := newTestVariance(t)
	assert.Equal(t, domain.VarianceStatusDetected, v.Status)

	// Transition through valuation flow: DETECTED -> OPEN -> INVESTIGATING -> RESOLVED
	v.Status = domain.VarianceStatusOpen
	require.NoError(t, v.Investigate())
	assert.Equal(t, domain.VarianceStatusInvestigating, v.Status)

	require.NoError(t, v.Resolve("Manual correction applied", "admin"))
	assert.Equal(t, domain.VarianceStatusResolved, v.Status)
	assert.Equal(t, "Manual correction applied", v.ResolutionNote)
	assert.Equal(t, "admin", v.ResolvedBy)
	assert.NotNil(t, v.ResolvedAt)
}

func TestVariance_DisputeLifecycle(t *testing.T) {
	v := newTestVariance(t)
	v.Status = domain.VarianceStatusOpen

	require.NoError(t, v.Dispute())
	assert.Equal(t, domain.VarianceStatusDisputed, v.Status)

	require.NoError(t, v.Investigate())
	assert.Equal(t, domain.VarianceStatusInvestigating, v.Status)

	require.NoError(t, v.Accept("Known timing difference", "reviewer"))
	assert.Equal(t, domain.VarianceStatusAccepted, v.Status)
	assert.NotNil(t, v.ResolvedAt)
}

func TestVariance_InvalidTransitions(t *testing.T) {
	t.Run("cannot investigate from detected", func(t *testing.T) {
		v := newTestVariance(t)
		err := v.Investigate()
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})

	t.Run("cannot investigate from resolved", func(t *testing.T) {
		v := newTestVariance(t)
		v.Status = domain.VarianceStatusOpen
		require.NoError(t, v.Resolve("done", "admin"))
		err := v.Investigate()
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})

	t.Run("cannot dispute from accepted", func(t *testing.T) {
		v := newTestVariance(t)
		v.Status = domain.VarianceStatusOpen
		require.NoError(t, v.Accept("accepted", "admin"))
		err := v.Dispute()
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})
}

func newTestVariance(t *testing.T) *domain.Variance {
	t.Helper()
	v, err := domain.NewVariance(
		uuid.New(), uuid.New(), "ACC-001", "GBP",
		decimal.NewFromFloat(1000.00), decimal.NewFromFloat(995.50),
		domain.VarianceReasonAmountMismatch,
	)
	require.NoError(t, err)
	return v
}
