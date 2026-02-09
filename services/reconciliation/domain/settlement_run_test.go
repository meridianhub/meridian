package domain_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSettlementRun(t *testing.T) {
	now := time.Now().UTC()
	periodStart := now.Add(-24 * time.Hour)
	periodEnd := now

	tests := []struct {
		name           string
		accountID      string
		scope          domain.ReconciliationScope
		settlementType domain.SettlementType
		periodStart    time.Time
		periodEnd      time.Time
		initiatedBy    string
		wantErr        error
	}{
		{
			name:           "valid daily run",
			accountID:      "ACC-001",
			scope:          domain.ReconciliationScopeAccount,
			settlementType: domain.SettlementTypeDaily,
			periodStart:    periodStart,
			periodEnd:      periodEnd,
			initiatedBy:    "system",
			wantErr:        nil,
		},
		{
			name:           "empty account ID",
			accountID:      "",
			scope:          domain.ReconciliationScopeAccount,
			settlementType: domain.SettlementTypeDaily,
			periodStart:    periodStart,
			periodEnd:      periodEnd,
			initiatedBy:    "system",
			wantErr:        domain.ErrEmptyAccountID,
		},
		{
			name:           "invalid scope",
			accountID:      "ACC-001",
			scope:          domain.ReconciliationScope("INVALID"),
			settlementType: domain.SettlementTypeDaily,
			periodStart:    periodStart,
			periodEnd:      periodEnd,
			initiatedBy:    "system",
			wantErr:        domain.ErrEmptyScope,
		},
		{
			name:           "invalid settlement type",
			accountID:      "ACC-001",
			scope:          domain.ReconciliationScopeAccount,
			settlementType: domain.SettlementType("INVALID"),
			periodStart:    periodStart,
			periodEnd:      periodEnd,
			initiatedBy:    "system",
			wantErr:        domain.ErrEmptySettlementType,
		},
		{
			name:           "invalid period (start after end)",
			accountID:      "ACC-001",
			scope:          domain.ReconciliationScopeAccount,
			settlementType: domain.SettlementTypeDaily,
			periodStart:    periodEnd,
			periodEnd:      periodStart,
			initiatedBy:    "system",
			wantErr:        domain.ErrInvalidPeriod,
		},
		{
			name:           "invalid period (equal start and end)",
			accountID:      "ACC-001",
			scope:          domain.ReconciliationScopeAccount,
			settlementType: domain.SettlementTypeDaily,
			periodStart:    periodStart,
			periodEnd:      periodStart,
			initiatedBy:    "system",
			wantErr:        domain.ErrInvalidPeriod,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, err := domain.NewSettlementRun(
				tt.accountID, tt.scope, tt.settlementType,
				tt.periodStart, tt.periodEnd, tt.initiatedBy,
			)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, run)
			} else {
				require.NoError(t, err)
				require.NotNil(t, run)

				assert.NotEqual(t, uuid.Nil, run.RunID)
				assert.Equal(t, tt.accountID, run.AccountID)
				assert.Equal(t, tt.scope, run.Scope)
				assert.Equal(t, tt.settlementType, run.SettlementType)
				assert.Equal(t, domain.RunStatusPending, run.Status)
				assert.Equal(t, int64(1), run.Version)
				assert.Nil(t, run.CompletedAt)
				assert.Equal(t, 0, run.VarianceCount)
				assert.Empty(t, run.FailureReason)
			}
		})
	}
}

func TestSettlementRun_Lifecycle(t *testing.T) {
	run := newTestRun(t)

	// Start
	require.NoError(t, run.Start())
	assert.Equal(t, domain.RunStatusRunning, run.Status)
	assert.Equal(t, int64(2), run.Version)

	// Complete
	require.NoError(t, run.Complete(5))
	assert.Equal(t, domain.RunStatusCompleted, run.Status)
	assert.Equal(t, 5, run.VarianceCount)
	assert.NotNil(t, run.CompletedAt)
	assert.Equal(t, int64(3), run.Version)
}

func TestSettlementRun_FailLifecycle(t *testing.T) {
	run := newTestRun(t)

	require.NoError(t, run.Start())
	require.NoError(t, run.Fail("database timeout"))

	assert.Equal(t, domain.RunStatusFailed, run.Status)
	assert.Equal(t, "database timeout", run.FailureReason)
	assert.NotNil(t, run.CompletedAt)
	assert.Equal(t, int64(3), run.Version)
}

func TestSettlementRun_CancelLifecycle(t *testing.T) {
	run := newTestRun(t)

	require.NoError(t, run.Cancel())
	assert.Equal(t, domain.RunStatusCancelled, run.Status)
	assert.NotNil(t, run.CompletedAt)
	assert.Equal(t, int64(2), run.Version)
}

func TestSettlementRun_InvalidTransitions(t *testing.T) {
	t.Run("cannot complete from pending", func(t *testing.T) {
		run := newTestRun(t)
		err := run.Complete(0)
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})

	t.Run("cannot fail from pending", func(t *testing.T) {
		run := newTestRun(t)
		err := run.Fail("reason")
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})

	t.Run("cannot start from completed", func(t *testing.T) {
		run := newTestRun(t)
		require.NoError(t, run.Start())
		require.NoError(t, run.Complete(0))
		err := run.Start()
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})

	t.Run("cannot cancel from completed", func(t *testing.T) {
		run := newTestRun(t)
		require.NoError(t, run.Start())
		require.NoError(t, run.Complete(0))
		err := run.Cancel()
		assert.ErrorIs(t, err, domain.ErrInvalidStatusTransition)
	})
}

func newTestRun(t *testing.T) *domain.SettlementRun {
	t.Helper()
	now := time.Now().UTC()
	run, err := domain.NewSettlementRun(
		"ACC-001",
		domain.ReconciliationScopeAccount,
		domain.SettlementTypeDaily,
		now.Add(-24*time.Hour),
		now,
		"test-user",
	)
	require.NoError(t, err)
	return run
}
