//go:build integration
// +build integration

package reconciliatione2e

import (
	"testing"

	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSettlementLifecycle_FullCycle exercises the full settlement run lifecycle:
// Initiate run -> capture snapshots (mocked PK) -> detect variances -> value variances -> finalize.
func TestSettlementLifecycle_FullCycle(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := contextWithServiceClaims(
		contextWithAdminClaims(infra.tenantCtx()),
	)

	// Step 1: Create a settlement run
	run := createSettlementRun(t, ctx, infra, "ACC-001", domain.ReconciliationScopeAccount, domain.SettlementTypeFinal, periodStart, periodEnd, "e2e-test")
	require.Equal(t, domain.RunStatusPending, run.Status)

	// Step 2: Set up mock position data for snapshot capture
	infra.mockPKProvider.setPages([]service.PositionPage{
		{
			Records: []service.PositionRecord{
				{
					AccountID:      "ACC-001",
					InstrumentCode: "GBP",
					Balance:        decimal.NewFromFloat(1000.50),
					SourceSystem:   "position-keeping",
					Attributes:     map[string]string{"quality": "ACTUAL"},
				},
				{
					AccountID:      "ACC-001",
					InstrumentCode: "EUR",
					Balance:        decimal.NewFromFloat(2500.00),
					SourceSystem:   "position-keeping",
					Attributes:     map[string]string{"quality": "ESTIMATE"},
				},
			},
			NextPageToken: "",
		},
	})

	// Step 3: Capture snapshots - run transitions PENDING -> RUNNING -> COMPLETED
	err := infra.capturer.CaptureSnapshots(ctx, run.RunID)
	require.NoError(t, err)

	// Verify run is now COMPLETED
	updatedRun, err := infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusCompleted, updatedRun.Status)
	assert.NotNil(t, updatedRun.CompletedAt)

	// Verify snapshots were persisted
	snapshots, err := infra.snapRepo.FindByRunID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Len(t, snapshots, 2)

	// Step 4: Create a second run with different actual values to produce variances
	// First, we need to complete the initial run's variances phase manually since
	// CaptureSnapshots sets expected == actual (no variance on first run).
	// Create a new run to compare against the first.
	run2 := createSettlementRun(t, ctx, infra, "ACC-001", domain.ReconciliationScopeAccount, domain.SettlementTypeFinal, periodStart, periodEnd, "e2e-test")

	// Mock PK returns updated balances for second run
	infra.mockPKProvider.setPages([]service.PositionPage{
		{
			Records: []service.PositionRecord{
				{
					AccountID:      "ACC-001",
					InstrumentCode: "GBP",
					Balance:        decimal.NewFromFloat(1050.75), // Changed from 1000.50
					SourceSystem:   "position-keeping",
					Attributes:     map[string]string{"quality": "ACTUAL"},
				},
				{
					AccountID:      "ACC-001",
					InstrumentCode: "EUR",
					Balance:        decimal.NewFromFloat(2500.00), // Same
					SourceSystem:   "position-keeping",
					Attributes:     map[string]string{"quality": "ACTUAL"},
				},
			},
			NextPageToken: "",
		},
	})

	// Capture snapshots for second run
	err = infra.capturer.CaptureSnapshots(ctx, run2.RunID)
	require.NoError(t, err)

	// Step 5: Detect variances on second run (compares against first run's snapshots)
	// The run must be in RUNNING state for variance detection, but CaptureSnapshots
	// already moved it to COMPLETED. We need to work with the run as the detector expects RUNNING.
	// Since the detector checks for RUNNING state, we create the run manually in RUNNING state.
	run3 := createSettlementRun(t, ctx, infra, "ACC-001", domain.ReconciliationScopeAccount, domain.SettlementTypeFinal, periodStart, periodEnd, "e2e-test")
	require.NoError(t, run3.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run3))

	// Create snapshots manually for run3 with variance-producing values
	createSnapshot(t, ctx, infra, run3.RunID, "ACC-001", "GBP",
		decimal.NewFromFloat(1000.50), // expected (from previous)
		decimal.NewFromFloat(1050.75), // actual (current)
		"position-keeping", map[string]string{"quality": "ACTUAL"})

	// Detect variances
	variances, err := infra.detector.DetectVariances(ctx, run3.RunID)
	require.NoError(t, err)
	assert.Len(t, variances, 1, "should detect 1 variance for GBP mismatch")

	// Verify the variance details
	v := variances[0]
	assert.Equal(t, "ACC-001", v.AccountID)
	assert.Equal(t, "GBP", v.InstrumentCode)
	assert.True(t, v.ExpectedAmount.Equal(decimal.NewFromFloat(1000.50)))
	assert.True(t, v.ActualAmount.Equal(decimal.NewFromFloat(1050.75)))
	assert.True(t, v.VarianceAmount.Equal(decimal.NewFromFloat(50.25)))
	assert.Equal(t, domain.VarianceStatusDetected, v.Status)

	// Step 6: Value variances
	err = infra.valuator.ValueVariances(ctx, run3.RunID)
	require.NoError(t, err)

	// Verify variance is now valued
	valuedVariance, err := infra.varianceRepo.FindByID(ctx, v.VarianceID)
	require.NoError(t, err)
	assert.Equal(t, domain.VarianceStatusValued, valuedVariance.Status)
	assert.False(t, valuedVariance.ValueDelta.IsZero(), "value delta should be set")
	assert.Equal(t, "GBP", valuedVariance.Currency)

	// Verify run's variance count was updated
	run3Updated, err := infra.runRepo.FindByID(ctx, run3.RunID)
	require.NoError(t, err)
	assert.Equal(t, 1, run3Updated.VarianceCount)

	// Step 7: Complete the run before finalization
	run3Refreshed, err := infra.runRepo.FindByID(ctx, run3.RunID)
	require.NoError(t, err)
	require.NoError(t, run3Refreshed.Complete(1))
	require.NoError(t, infra.runRepo.Update(ctx, run3Refreshed))

	// Step 8: Finalize with position lock (requires service role)
	err = infra.finalizer.FinalizeSettlement(ctx, run3.RunID)
	require.NoError(t, err)

	// Verify run is FINALIZED
	finalRun, err := infra.runRepo.FindByID(ctx, run3.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusFinalized, finalRun.Status)

	// Verify lock was requested
	lockCalls := infra.mockLockClient.getLockCalls()
	assert.Len(t, lockCalls, 1)
	assert.Equal(t, run3.RunID, lockCalls[0].RunID)

	// Verify finalization event was published
	lockEvents := infra.mockPublisher.getEventsByTopic("reconciliation.position-lock-requested.v1")
	assert.Len(t, lockEvents, 1)
}

// TestSettlementLifecycle_D1RunNoVariances tests a D+1 first-time run where
// expected == actual produces zero variances.
func TestSettlementLifecycle_D1RunNoVariances(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := contextWithServiceClaims(infra.tenantCtx())

	// Create a run and move to RUNNING
	run := createSettlementRun(t, ctx, infra, "ACC-D1", domain.ReconciliationScopeAccount, domain.SettlementTypeDaily, periodStart, periodEnd, "e2e-scheduler")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Create snapshots with no variance (expected == actual)
	createSnapshot(t, ctx, infra, run.RunID, "ACC-D1", "GBP",
		decimal.NewFromFloat(500.00),
		decimal.NewFromFloat(500.00),
		"position-keeping", nil)

	// Detect variances - D+1 run with no previous run to compare against
	variances, err := infra.detector.DetectVariances(ctx, run.RunID)
	require.NoError(t, err)
	assert.Empty(t, variances, "D+1 run with equal expected/actual should produce no variances")
}

// TestSettlementLifecycle_CaptureFailure verifies that a PK fetch failure
// transitions the run to FAILED state.
func TestSettlementLifecycle_CaptureFailure(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := infra.tenantCtx()

	run := createSettlementRun(t, ctx, infra, "ACC-FAIL", domain.ReconciliationScopeAccount, domain.SettlementTypeDaily, periodStart, periodEnd, "e2e-test")

	// Mock PK to return an error
	infra.mockPKProvider.setError(assert.AnError)

	err := infra.capturer.CaptureSnapshots(ctx, run.RunID)
	require.Error(t, err)

	// Verify run is FAILED
	failedRun, err := infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusFailed, failedRun.Status)
	assert.NotEmpty(t, failedRun.FailureReason)
	assert.NotNil(t, failedRun.CompletedAt)
}

// TestSettlementLifecycle_ValuationBelowMateriality verifies that variances
// below the materiality threshold are auto-accepted.
func TestSettlementLifecycle_ValuationBelowMateriality(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := infra.tenantCtx()

	// Set high materiality threshold so our variance is below it
	infra.mockRefData.setThreshold("GBP", decimal.NewFromFloat(1000.00))

	// Create a RUNNING run with a small variance
	run := createSettlementRun(t, ctx, infra, "ACC-MAT", domain.ReconciliationScopeAccount, domain.SettlementTypeDaily, periodStart, periodEnd, "e2e-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Create variance with small amount (below materiality)
	snap := createSnapshot(t, ctx, infra, run.RunID, "ACC-MAT", "GBP",
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(100.05),
		"position-keeping", nil)

	createVariance(t, ctx, infra, run.RunID, snap.SnapshotID,
		"ACC-MAT", "GBP",
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(100.05),
		domain.VarianceReasonAmountMismatch)

	// Value variances
	err := infra.valuator.ValueVariances(ctx, run.RunID)
	require.NoError(t, err)

	// Verify variance was auto-accepted (below materiality)
	variances, err := infra.varianceRepo.FindByRunID(ctx, run.RunID)
	require.NoError(t, err)
	require.Len(t, variances, 1)
	assert.Equal(t, domain.VarianceStatusAccepted, variances[0].Status, "variance below materiality should be auto-accepted")
	assert.Contains(t, variances[0].ResolutionNote, "auto-accepted")
	assert.Equal(t, "system:materiality-filter", variances[0].ResolvedBy)
}

// TestSettlementLifecycle_FinalizeRequiresServiceRole verifies RBAC on finalization.
func TestSettlementLifecycle_FinalizeRequiresServiceRole(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := infra.tenantCtx()

	// Create and complete a FINAL run
	run := createSettlementRun(t, ctx, infra, "ACC-RBAC", domain.ReconciliationScopeAccount, domain.SettlementTypeFinal, periodStart, periodEnd, "e2e-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))
	run, _ = infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, run.Complete(0))
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Try to finalize with admin role (should fail - requires service role)
	adminCtx := contextWithAdminClaims(ctx)
	err := infra.finalizer.FinalizeSettlement(adminCtx, run.RunID)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)

	// Try with auditor role (should also fail)
	auditorCtx := contextWithAuditorClaims(ctx)
	err = infra.finalizer.FinalizeSettlement(auditorCtx, run.RunID)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)

	// Service role should succeed
	serviceCtx := contextWithServiceClaims(ctx)
	err = infra.finalizer.FinalizeSettlement(serviceCtx, run.RunID)
	require.NoError(t, err)

	finalRun, err := infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusFinalized, finalRun.Status)
}

// TestSettlementLifecycle_FinalizeIdempotent verifies that finalizing an
// already finalized run returns success.
func TestSettlementLifecycle_FinalizeIdempotent(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := contextWithServiceClaims(infra.tenantCtx())

	// Create, complete, and finalize a run
	run := createSettlementRun(t, ctx, infra, "ACC-IDEMP", domain.ReconciliationScopeAccount, domain.SettlementTypeFinal, periodStart, periodEnd, "e2e-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))
	run, _ = infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, run.Complete(0))
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Finalize first time
	err := infra.finalizer.FinalizeSettlement(ctx, run.RunID)
	require.NoError(t, err)

	// Reset publisher to track second call
	infra.mockPublisher.reset()

	// Finalize second time - should be idempotent (no error, no new events)
	err = infra.finalizer.FinalizeSettlement(ctx, run.RunID)
	require.NoError(t, err)

	// No new lock events should be published for idempotent call
	lockEvents := infra.mockPublisher.getEventsByTopic("reconciliation.position-lock-requested.v1")
	assert.Empty(t, lockEvents, "idempotent finalization should not publish new events")
}

// TestSettlementLifecycle_FinalizeRejectsNonFinalType verifies that only FINAL
// settlement type runs can be finalized.
func TestSettlementLifecycle_FinalizeRejectsNonFinalType(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := contextWithServiceClaims(infra.tenantCtx())

	// Create a DAILY (non-FINAL) run and complete it
	run := createSettlementRun(t, ctx, infra, "ACC-NONFINAL", domain.ReconciliationScopeAccount, domain.SettlementTypeDaily, periodStart, periodEnd, "e2e-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))
	run, _ = infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, run.Complete(0))
	require.NoError(t, infra.runRepo.Update(ctx, run))

	err := infra.finalizer.FinalizeSettlement(ctx, run.RunID)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrNotFinalSettlement)
}

// TestSettlementLifecycle_OptimisticLocking verifies that concurrent updates
// on the same run are detected via version conflicts.
func TestSettlementLifecycle_OptimisticLocking(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := infra.tenantCtx()

	run := createSettlementRun(t, ctx, infra, "ACC-LOCK", domain.ReconciliationScopeAccount, domain.SettlementTypeDaily, periodStart, periodEnd, "e2e-test")

	// Load two copies of the same run
	run1, err := infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	run2, err := infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)

	// Start both - first succeeds
	require.NoError(t, run1.Start())
	err = infra.runRepo.Update(ctx, run1)
	require.NoError(t, err)

	// Second should fail with optimistic lock error
	require.NoError(t, run2.Start())
	err = infra.runRepo.Update(ctx, run2)
	require.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrOptimisticLock)
}
