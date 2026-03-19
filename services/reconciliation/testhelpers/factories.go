// Package testhelpers provides shared test factories and utilities for reconciliation tests.
package testhelpers

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// NewSettlementRun creates a standard settlement run for testing.
// The run is in PENDING status with daily scope for account ACC-001.
func NewSettlementRun(t *testing.T) *domain.SettlementRun {
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

// NewRunningSettlementRun creates a settlement run that has been started.
func NewRunningSettlementRun(t *testing.T) *domain.SettlementRun {
	t.Helper()
	run := NewSettlementRun(t)
	err := run.Start()
	require.NoError(t, err)
	return run
}

// NewBalanceAssertion creates a standard balance assertion for testing.
func NewBalanceAssertion(t *testing.T) *domain.BalanceAssertion {
	t.Helper()
	runID := uuid.New()
	a, err := domain.NewBalanceAssertion(
		&runID, "ACC-001", "GBP",
		"balance == 10000", decimal.NewFromFloat(10000.00),
	)
	require.NoError(t, err)
	return a
}

// NewDispute creates a standard dispute for testing.
func NewDispute(t *testing.T) *domain.Dispute {
	t.Helper()
	d, err := domain.NewDispute(
		uuid.New(), uuid.New(), "ACC-001",
		"Amount discrepancy noted", "user-1",
	)
	require.NoError(t, err)
	return d
}

// NewVariance creates a standard variance for testing with the given run and snapshot IDs.
func NewVariance(t *testing.T, runSurrogateID, snapshotSurrogateID uuid.UUID) *domain.Variance {
	t.Helper()
	v, err := domain.NewVariance(
		runSurrogateID,
		snapshotSurrogateID,
		"ACC-001", "GBP",
		decimal.NewFromFloat(1000.00), decimal.NewFromFloat(995.50),
		domain.VarianceReasonAmountMismatch,
	)
	require.NoError(t, err)
	return v
}

// NewDefaultVariance creates a variance with generated IDs for simple test cases.
func NewDefaultVariance(t *testing.T) *domain.Variance {
	t.Helper()
	return NewVariance(t, uuid.New(), uuid.New())
}
