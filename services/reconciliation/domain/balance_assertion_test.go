package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewBalanceAssertion(t *testing.T) {
	runID := uuid.New()

	tests := []struct {
		name           string
		runID          *uuid.UUID
		accountID      string
		instrumentCode string
		expression     string
		expected       decimal.Decimal
		wantErr        error
	}{
		{
			name:           "valid assertion with run",
			runID:          &runID,
			accountID:      "ACC-001",
			instrumentCode: "GBP",
			expression:     "sum(positions) == expected",
			expected:       decimal.NewFromFloat(10000.00),
			wantErr:        nil,
		},
		{
			name:           "valid standalone assertion",
			runID:          nil,
			accountID:      "ACC-001",
			instrumentCode: "GBP",
			expression:     "total_debits == total_credits",
			expected:       decimal.Zero,
			wantErr:        nil,
		},
		{
			name:           "empty account ID",
			runID:          &runID,
			accountID:      "",
			instrumentCode: "GBP",
			expression:     "expr",
			expected:       decimal.Zero,
			wantErr:        domain.ErrEmptyAccountID,
		},
		{
			name:           "empty instrument code",
			runID:          &runID,
			accountID:      "ACC-001",
			instrumentCode: "",
			expression:     "expr",
			expected:       decimal.Zero,
			wantErr:        domain.ErrEmptyInstrumentCode,
		},
		{
			name:           "empty expression",
			runID:          &runID,
			accountID:      "ACC-001",
			instrumentCode: "GBP",
			expression:     "",
			expected:       decimal.Zero,
			wantErr:        domain.ErrEmptyAssertionExpression,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := domain.NewBalanceAssertion(
				tt.runID, tt.accountID, tt.instrumentCode,
				tt.expression, tt.expected,
			)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, a)
			} else {
				require.NoError(t, err)
				require.NotNil(t, a)

				assert.NotEqual(t, uuid.Nil, a.AssertionID)
				assert.Equal(t, tt.runID, a.RunID)
				assert.Equal(t, tt.accountID, a.AccountID)
				assert.Equal(t, tt.instrumentCode, a.InstrumentCode)
				assert.Equal(t, tt.expression, a.Expression)
				assert.Equal(t, domain.AssertionStatusPending, a.Status)
				assert.True(t, a.AssertedAt.IsZero(), "AssertedAt should be zero until evaluation")
			}
		})
	}
}

func TestBalanceAssertion_Pass(t *testing.T) {
	a := newTestAssertion(t)

	require.NoError(t, a.Pass(decimal.NewFromFloat(10000.00)))
	assert.Equal(t, domain.AssertionStatusPassed, a.Status)
	assert.True(t, decimal.NewFromFloat(10000.00).Equal(a.ActualBalance))
	assert.False(t, a.AssertedAt.IsZero(), "AssertedAt should be set after Pass")
}

func TestBalanceAssertion_Fail(t *testing.T) {
	a := newTestAssertion(t)

	require.NoError(t, a.Fail(decimal.NewFromFloat(9500.00), "Balance mismatch: expected 10000, got 9500"))
	assert.Equal(t, domain.AssertionStatusFailed, a.Status)
	assert.True(t, decimal.NewFromFloat(9500.00).Equal(a.ActualBalance))
	assert.Equal(t, "Balance mismatch: expected 10000, got 9500", a.FailureReason)
	assert.False(t, a.AssertedAt.IsZero(), "AssertedAt should be set after Fail")
}

func TestBalanceAssertion_Override(t *testing.T) {
	a := newTestAssertion(t)

	require.NoError(t, a.Fail(decimal.NewFromFloat(9500.00), "mismatch"))
	require.NoError(t, a.Override("approved by manager"))

	assert.Equal(t, domain.AssertionStatusOverride, a.Status)
	assert.Equal(t, "mismatch", a.FailureReason, "original failure reason should be preserved")
	assert.Equal(t, "approved by manager", a.OverrideReason)
}

func TestBalanceAssertion_OverrideEmptyReason(t *testing.T) {
	a := newTestAssertion(t)
	require.NoError(t, a.Fail(decimal.NewFromFloat(9500.00), "mismatch"))
	err := a.Override("")
	assert.ErrorIs(t, err, domain.ErrEmptyOverrideReason)
}

func TestBalanceAssertion_OverrideFromNonFailed(t *testing.T) {
	t.Run("cannot override from pending", func(t *testing.T) {
		a := newTestAssertion(t)
		err := a.Override("reason")
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})

	t.Run("cannot override from passed", func(t *testing.T) {
		a := newTestAssertion(t)
		require.NoError(t, a.Pass(decimal.NewFromFloat(10000.00)))
		err := a.Override("reason")
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})
}

func TestBalanceAssertion_InvalidTransitions(t *testing.T) {
	t.Run("cannot pass from passed", func(t *testing.T) {
		a := newTestAssertion(t)
		require.NoError(t, a.Pass(decimal.NewFromFloat(10000.00)))
		err := a.Pass(decimal.NewFromFloat(10000.00))
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})

	t.Run("cannot fail from failed", func(t *testing.T) {
		a := newTestAssertion(t)
		require.NoError(t, a.Fail(decimal.NewFromFloat(9500.00), "mismatch"))
		err := a.Fail(decimal.NewFromFloat(9500.00), "mismatch")
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})

	t.Run("cannot pass from failed", func(t *testing.T) {
		a := newTestAssertion(t)
		require.NoError(t, a.Fail(decimal.NewFromFloat(9500.00), "mismatch"))
		err := a.Pass(decimal.NewFromFloat(10000.00))
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})
}

func newTestAssertion(t *testing.T) *domain.BalanceAssertion {
	t.Helper()
	runID := uuid.New()
	a, err := domain.NewBalanceAssertion(
		&runID, "ACC-001", "GBP",
		"balance == 10000", decimal.NewFromFloat(10000.00),
	)
	require.NoError(t, err)
	return a
}
