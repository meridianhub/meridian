//go:build integration
// +build integration

package reconciliatione2e

import (
	"testing"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// =============================================================================
// Phase 2 E2E: Balance Assertion via gRPC
// =============================================================================

// TestBalanceAssertionGRPC_Balanced tests the AssertBalance RPC for a balanced case.
// Verifies: assertion persisted, PASSED status, no imbalance event.
func TestBalanceAssertionGRPC_Balanced(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	// Setup: mock PK with balanced positions
	infra.mockPKClient.setSummary("ACC-GRPC-BAL", "GBP",
		decimal.NewFromFloat(5000.00),
		decimal.NewFromFloat(5000.00),
	)

	// Call AssertBalance RPC
	resp, err := infra.grpcClient.AssertBalance(ctx, &reconciliationv1.AssertBalanceRequest{
		AccountId:       "ACC-GRPC-BAL",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: "5000",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Assertion)

	assert.Equal(t, reconciliationv1.AssertionStatus_ASSERTION_STATUS_PASSED, resp.Assertion.Status)
	assert.NotEmpty(t, resp.Assertion.AssertionId)

	// Verify assertion persisted in DB
	assertionID, err := uuid.Parse(resp.Assertion.AssertionId)
	require.NoError(t, err)
	persisted, err := infra.assertionRepo.FindByID(ctx, assertionID)
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusPassed, persisted.Status)

	// Verify no imbalance event published
	imbalanceEvents := infra.mockPublisher.getEventsByTopic("reconciliation.balance-imbalance-detected.v1")
	assert.Empty(t, imbalanceEvents, "no imbalance event for balanced positions")
}

// TestBalanceAssertionGRPC_Imbalanced tests the AssertBalance RPC for an imbalanced case.
// Verifies: assertion persisted, FAILED status, imbalance event published, trend tracking.
func TestBalanceAssertionGRPC_Imbalanced(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	// Setup: mock PK with imbalance
	infra.mockPKClient.setSummary("ACC-GRPC-IMBAL", "GBP",
		decimal.NewFromFloat(10000.00),
		decimal.NewFromFloat(9500.00), // 500 GBP imbalance
	)

	resp, err := infra.grpcClient.AssertBalance(ctx, &reconciliationv1.AssertBalanceRequest{
		AccountId:       "ACC-GRPC-IMBAL",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: "10000",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.Assertion)

	assert.Equal(t, reconciliationv1.AssertionStatus_ASSERTION_STATUS_FAILED, resp.Assertion.Status)

	// Verify assertion persisted
	assertionID, err := uuid.Parse(resp.Assertion.AssertionId)
	require.NoError(t, err)
	persisted, err := infra.assertionRepo.FindByID(ctx, assertionID)
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusFailed, persisted.Status)

	// Verify imbalance event was published
	imbalanceEvents := infra.mockPublisher.getEventsByTopic("reconciliation.balance-imbalance-detected.v1")
	require.Len(t, imbalanceEvents, 1)

	// Verify trend tracking was started
	trend, err := infra.trendRepo.FindByInstrumentCode(ctx, "GBP")
	require.NoError(t, err)
	assert.Equal(t, "GBP", trend.InstrumentCode)
	assert.Greater(t, trend.ConsecutiveDays, 0)
}

// TestBalanceAssertionGRPC_InvalidInput tests validation on AssertBalance RPC.
func TestBalanceAssertionGRPC_InvalidInput(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	tests := []struct {
		name         string
		req          *reconciliationv1.AssertBalanceRequest
		expectedCode codes.Code
	}{
		{
			name: "invalid expected_balance",
			req: &reconciliationv1.AssertBalanceRequest{
				AccountId:       "ACC-001",
				InstrumentCode:  "GBP",
				Expression:      "total_debits == total_credits",
				ExpectedBalance: "not-a-number",
			},
			expectedCode: codes.InvalidArgument,
		},
		{
			name: "invalid run_id",
			req: &reconciliationv1.AssertBalanceRequest{
				AccountId:       "ACC-001",
				InstrumentCode:  "GBP",
				Expression:      "total_debits == total_credits",
				ExpectedBalance: "1000",
				RunId:           "not-a-uuid",
			},
			expectedCode: codes.InvalidArgument,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := infra.grpcClient.AssertBalance(ctx, tc.req)
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok)
			assert.Equal(t, tc.expectedCode, st.Code())
		})
	}
}

// TestBalanceAssertionGRPC_WithRunID tests that AssertBalance can link to a specific run.
func TestBalanceAssertionGRPC_WithRunID(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	// Create a run to link the assertion to
	run := createSettlementRun(t, ctx, infra, "ACC-GRPC-RUNLINK",
		domain.ReconciliationScopeAccount, domain.SettlementTypeDaily,
		periodStart, periodEnd, "e2e-test")

	infra.mockPKClient.setSummary("ACC-GRPC-RUNLINK", "EUR",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(1000.00),
	)

	resp, err := infra.grpcClient.AssertBalance(ctx, &reconciliationv1.AssertBalanceRequest{
		AccountId:       "ACC-GRPC-RUNLINK",
		InstrumentCode:  "EUR",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: "1000",
		RunId:           run.RunID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.AssertionStatus_ASSERTION_STATUS_PASSED, resp.Assertion.Status)

	// Verify assertion linked to run in DB
	assertionID, err := uuid.Parse(resp.Assertion.AssertionId)
	require.NoError(t, err)
	persisted, err := infra.assertionRepo.FindByID(ctx, assertionID)
	require.NoError(t, err)
	require.NotNil(t, persisted.RunID)
	assert.Equal(t, run.RunID, *persisted.RunID)
}

// =============================================================================
// Phase 2 E2E: Kafka Event Publishing (Mock Publisher)
// =============================================================================

// TestKafkaEventPublishing_FullPipeline tests that events are published at each stage
// of the reconciliation pipeline when the publisher is wired.
// Uses direct service component calls to exercise the pipeline phases, since the
// async pipeline goroutine uses context.Background() which loses the tenant search_path.
func TestKafkaEventPublishing_FullPipeline(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := contextWithServiceClaims(infra.tenantCtx())
	periodStart, periodEnd := defaultPeriod()

	// Create a run and transition to RUNNING
	run := createSettlementRun(t, ctx, infra, "ACC-KAFKA-001",
		domain.ReconciliationScopeAccount, domain.SettlementTypeFinal,
		periodStart, periodEnd, "e2e-kafka-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Create snapshots with a variance
	createSnapshot(t, ctx, infra, run.RunID, "ACC-KAFKA-001", "GBP",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(1050.00),
		"position-keeping", map[string]string{"quality": "ACTUAL"})

	infra.mockPublisher.reset()

	// Phase 1: Detect variances
	variances, err := infra.detector.DetectVariances(ctx, run.RunID)
	require.NoError(t, err)
	assert.Len(t, variances, 1)

	// Phase 2: Value variances
	err = infra.valuator.ValueVariances(ctx, run.RunID)
	require.NoError(t, err)

	// Phase 3: Complete and finalize
	run, err = infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	require.NoError(t, run.Complete(1))
	require.NoError(t, infra.runRepo.Update(ctx, run))

	err = infra.finalizer.FinalizeSettlement(ctx, run.RunID)
	require.NoError(t, err)

	// Verify finalization event was published through the mock publisher
	lockEvents := infra.mockPublisher.getEventsByTopic("reconciliation.position-lock-requested.v1")
	require.Len(t, lockEvents, 1, "finalizer should publish a lock event via the publisher")

	// Verify final state
	finalRun, err := infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusFinalized, finalRun.Status)
}

// TestNoopPublisher_DisabledKafka verifies that the pipeline completes when Kafka
// is not configured (using the noop/mock publisher that never errors).
// Uses direct service component calls to exercise the pipeline phases.
func TestNoopPublisher_DisabledKafka(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := contextWithServiceClaims(infra.tenantCtx())
	periodStart, periodEnd := defaultPeriod()

	// The mock publisher acts as a noop - it records events but never errors.
	// This simulates the noop publisher scenario where Kafka is disabled.

	// Create a FINAL run (required for finalization) and transition to RUNNING
	run := createSettlementRun(t, ctx, infra, "ACC-NOOP-001",
		domain.ReconciliationScopeAccount, domain.SettlementTypeFinal,
		periodStart, periodEnd, "e2e-noop-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Create snapshots with matching balances (no variance)
	createSnapshot(t, ctx, infra, run.RunID, "ACC-NOOP-001", "GBP",
		decimal.NewFromFloat(500.00),
		decimal.NewFromFloat(500.00),
		"position-keeping", nil)

	// Run pipeline phases directly
	variances, err := infra.detector.DetectVariances(ctx, run.RunID)
	require.NoError(t, err)
	assert.Empty(t, variances, "balanced snapshots should produce no variances")

	// Complete the run
	run, err = infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	require.NoError(t, run.Complete(0))
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Finalize with noop publisher - should not error
	err = infra.finalizer.FinalizeSettlement(ctx, run.RunID)
	require.NoError(t, err, "finalization should succeed even with noop publisher")

	// Verify final state
	finalRun, err := infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusFinalized, finalRun.Status)
}

// =============================================================================
// Phase 2 E2E: Full Pipeline with Valuation
// =============================================================================

// TestFullPipeline_WithValuation tests the full reconciliation pipeline including
// valuation of detected variances.
func TestFullPipeline_WithValuation(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := contextWithServiceClaims(infra.tenantCtx())
	periodStart, periodEnd := defaultPeriod()

	// Setup: Create a RUNNING run with snapshots that will produce variances
	run := createSettlementRun(t, ctx, infra, "ACC-FULLPIPE",
		domain.ReconciliationScopeAccount, domain.SettlementTypeFinal,
		periodStart, periodEnd, "e2e-fullpipe-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Create snapshots with variance-producing values
	snap1 := createSnapshot(t, ctx, infra, run.RunID, "ACC-FULLPIPE", "GBP",
		decimal.NewFromFloat(10000.00), // expected
		decimal.NewFromFloat(10500.00), // actual (500 GBP variance)
		"position-keeping", map[string]string{"quality": "ACTUAL"})

	snap2 := createSnapshot(t, ctx, infra, run.RunID, "ACC-FULLPIPE", "EUR",
		decimal.NewFromFloat(5000.00),
		decimal.NewFromFloat(5000.00), // no variance
		"position-keeping", map[string]string{"quality": "ACTUAL"})
	_ = snap2

	// Step 1: Detect variances
	variances, err := infra.detector.DetectVariances(ctx, run.RunID)
	require.NoError(t, err)
	assert.Len(t, variances, 1, "should detect 1 variance for GBP mismatch")

	v := variances[0]
	assert.Equal(t, "ACC-FULLPIPE", v.AccountID)
	assert.Equal(t, "GBP", v.InstrumentCode)
	assert.Equal(t, snap1.SnapshotID, v.SnapshotID)
	assert.Equal(t, domain.VarianceStatusDetected, v.Status)

	// Step 2: Value variances (mock engine returns 1:1 GBP conversion)
	err = infra.valuator.ValueVariances(ctx, run.RunID)
	require.NoError(t, err)

	// Verify: Variances are now VALUED (not stuck in DETECTED)
	valuedVariances, err := infra.varianceRepo.FindByRunID(ctx, run.RunID)
	require.NoError(t, err)
	require.Len(t, valuedVariances, 1)
	assert.NotEqual(t, domain.VarianceStatusDetected, valuedVariances[0].Status,
		"variance should not be stuck in DETECTED after valuation")
	assert.False(t, valuedVariances[0].ValueDelta.IsZero(), "value delta should be set")
	assert.Equal(t, "GBP", valuedVariances[0].Currency)

	// Verify: Run summary updated with variance count
	run, err = infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, 1, run.VarianceCount)

	// Step 3: Complete and finalize
	require.NoError(t, run.Complete(1))
	require.NoError(t, infra.runRepo.Update(ctx, run))

	err = infra.finalizer.FinalizeSettlement(ctx, run.RunID)
	require.NoError(t, err)

	finalRun, err := infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusFinalized, finalRun.Status)
}

// TestFullPipeline_ValuationEngineFailure verifies that a valuation engine failure
// does not leave variances in an inconsistent state.
func TestFullPipeline_ValuationEngineFailure(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	// Create a run with a variance
	run := createSettlementRun(t, ctx, infra, "ACC-VALFAIL",
		domain.ReconciliationScopeAccount, domain.SettlementTypeDaily,
		periodStart, periodEnd, "e2e-valfail")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	snap := createSnapshot(t, ctx, infra, run.RunID, "ACC-VALFAIL", "GBP",
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(200.00),
		"position-keeping", nil)

	createVariance(t, ctx, infra, run.RunID, snap.SnapshotID,
		"ACC-VALFAIL", "GBP",
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(200.00),
		domain.VarianceReasonAmountMismatch)

	// Make the valuation engine fail
	infra.mockValEngine.mu.Lock()
	infra.mockValEngine.err = assert.AnError
	infra.mockValEngine.mu.Unlock()

	// Valuation should fail
	err := infra.valuator.ValueVariances(ctx, run.RunID)
	require.Error(t, err)

	// Variances should remain in DETECTED (not corrupted)
	variances, err := infra.varianceRepo.FindByRunID(ctx, run.RunID)
	require.NoError(t, err)
	for _, v := range variances {
		assert.Equal(t, domain.VarianceStatusDetected, v.Status,
			"variance should remain DETECTED after valuation failure")
	}

	// Reset engine and retry - should succeed
	infra.mockValEngine.mu.Lock()
	infra.mockValEngine.err = nil
	infra.mockValEngine.mu.Unlock()

	err = infra.valuator.ValueVariances(ctx, run.RunID)
	require.NoError(t, err)

	variances, err = infra.varianceRepo.FindByRunID(ctx, run.RunID)
	require.NoError(t, err)
	for _, v := range variances {
		assert.NotEqual(t, domain.VarianceStatusDetected, v.Status,
			"variance should be valued after retry")
	}
}

// =============================================================================
// Phase 2 E2E: PAUSE/RESUME Control Actions
// =============================================================================

// TestPauseResume_FullCycle tests the PAUSE/RESUME lifecycle via gRPC:
// Initiate -> Start -> PAUSE via gRPC -> verify PAUSED -> RESUME via gRPC
// -> verify RUNNING -> complete manually.
// Uses domain model transitions for state setup and gRPC for control actions,
// since the async pipeline goroutine has a known search_path limitation in tests.
func TestPauseResume_FullCycle(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	// Initiate via gRPC
	initiateResp, err := infra.grpcClient.InitiateAccountReconciliation(ctx,
		&reconciliationv1.InitiateAccountReconciliationRequest{
			AccountId:      "ACC-PAUSE-001",
			Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
			SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
			PeriodStart:    timestamppb.New(periodStart),
			PeriodEnd:      timestamppb.New(periodEnd),
			InitiatedBy:    "e2e-pause-test",
		})
	require.NoError(t, err)
	runID := initiateResp.Run.RunId

	// Transition to RUNNING via domain model (simulating pipeline start)
	parsedRunID, err := uuid.Parse(runID)
	require.NoError(t, err)
	run, err := infra.runRepo.FindByID(ctx, parsedRunID)
	require.NoError(t, err)
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Set a checkpoint to simulate that snapshot capture phase completed
	run, err = infra.runRepo.FindByID(ctx, parsedRunID)
	require.NoError(t, err)
	run.SetCheckpoint(domain.PhaseSnapshotCapture)
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// PAUSE via gRPC
	pauseResp, err := infra.grpcClient.ControlAccountReconciliation(ctx,
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  runID,
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_PAUSE,
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_PAUSED, pauseResp.Run.Status)

	// Verify PAUSED state persisted via Retrieve
	retrieveResp, err := infra.grpcClient.RetrieveAccountReconciliation(ctx,
		&reconciliationv1.RetrieveAccountReconciliationRequest{RunId: runID})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_PAUSED, retrieveResp.Run.Status)

	// Verify checkpoint was preserved
	pausedRun, err := infra.runRepo.FindByID(ctx, parsedRunID)
	require.NoError(t, err)
	require.NotNil(t, pausedRun.LastCompletedPhase)
	assert.Equal(t, domain.PhaseSnapshotCapture, *pausedRun.LastCompletedPhase)

	// RESUME via gRPC
	resumeResp, err := infra.grpcClient.ControlAccountReconciliation(ctx,
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  runID,
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_RESUME,
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_RUNNING, resumeResp.Run.Status)

	// Verify RUNNING state persisted
	retrieveResp2, err := infra.grpcClient.RetrieveAccountReconciliation(ctx,
		&reconciliationv1.RetrieveAccountReconciliationRequest{RunId: runID})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_RUNNING, retrieveResp2.Run.Status)
}

// TestPauseResume_InvalidTransitions tests that PAUSE/RESUME are rejected for invalid states.
func TestPauseResume_InvalidTransitions(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	t.Run("pause_pending_run", func(t *testing.T) {
		// Cannot PAUSE a PENDING run (only RUNNING can be paused)
		initiateResp, err := infra.grpcClient.InitiateAccountReconciliation(ctx,
			&reconciliationv1.InitiateAccountReconciliationRequest{
				AccountId:      "ACC-PPEND",
				Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
				SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
				PeriodStart:    timestamppb.New(periodStart),
				PeriodEnd:      timestamppb.New(periodEnd),
				InitiatedBy:    "e2e-test",
			})
		require.NoError(t, err)

		_, err = infra.grpcClient.ControlAccountReconciliation(ctx,
			&reconciliationv1.ControlAccountReconciliationRequest{
				RunId:  initiateResp.Run.RunId,
				Action: reconciliationv1.ControlAction_CONTROL_ACTION_PAUSE,
			})
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("resume_pending_run", func(t *testing.T) {
		// Cannot RESUME a PENDING run (only PAUSED can be resumed)
		initiateResp, err := infra.grpcClient.InitiateAccountReconciliation(ctx,
			&reconciliationv1.InitiateAccountReconciliationRequest{
				AccountId:      "ACC-RPEND",
				Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
				SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
				PeriodStart:    timestamppb.New(periodStart),
				PeriodEnd:      timestamppb.New(periodEnd),
				InitiatedBy:    "e2e-test",
			})
		require.NoError(t, err)

		_, err = infra.grpcClient.ControlAccountReconciliation(ctx,
			&reconciliationv1.ControlAccountReconciliationRequest{
				RunId:  initiateResp.Run.RunId,
				Action: reconciliationv1.ControlAction_CONTROL_ACTION_RESUME,
			})
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("resume_cancelled_run", func(t *testing.T) {
		// Cannot RESUME a CANCELLED run
		initiateResp, err := infra.grpcClient.InitiateAccountReconciliation(ctx,
			&reconciliationv1.InitiateAccountReconciliationRequest{
				AccountId:      "ACC-RCANC",
				Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
				SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
				PeriodStart:    timestamppb.New(periodStart),
				PeriodEnd:      timestamppb.New(periodEnd),
				InitiatedBy:    "e2e-test",
			})
		require.NoError(t, err)

		// Cancel first
		_, err = infra.grpcClient.ControlAccountReconciliation(ctx,
			&reconciliationv1.ControlAccountReconciliationRequest{
				RunId:  initiateResp.Run.RunId,
				Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
			})
		require.NoError(t, err)

		// Try to RESUME the cancelled run
		_, err = infra.grpcClient.ControlAccountReconciliation(ctx,
			&reconciliationv1.ControlAccountReconciliationRequest{
				RunId:  initiateResp.Run.RunId,
				Action: reconciliationv1.ControlAction_CONTROL_ACTION_RESUME,
			})
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})
}

// TestPauseResume_CancelFromPaused verifies that a paused run can be cancelled.
func TestPauseResume_CancelFromPaused(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	// Create a run, start it, and pause it directly via domain model
	run := createSettlementRun(t, ctx, infra, "ACC-PCANCEL",
		domain.ReconciliationScopeAccount, domain.SettlementTypeDaily,
		periodStart, periodEnd, "e2e-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Pause directly
	run, err := infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	checkpoint := domain.PhaseSnapshotCapture
	require.NoError(t, run.Pause(&checkpoint))
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Cancel from PAUSED via gRPC
	cancelResp, err := infra.grpcClient.ControlAccountReconciliation(ctx,
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  run.RunID.String(),
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_CANCELLED, cancelResp.Run.Status)
}

// =============================================================================
// Phase 2 E2E: Cross-Feature Integration
// =============================================================================

// TestCrossFeature_PipelineWithAssertionAndEvents exercises the full Phase 2 integration:
// Pipeline execution -> valuation -> events -> balance assertion.
func TestCrossFeature_PipelineWithAssertionAndEvents(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := contextWithServiceClaims(
		contextWithAdminClaims(infra.tenantCtx()),
	)
	periodStart, periodEnd := defaultPeriod()

	// Phase 1: Execute pipeline producing variances
	run := createSettlementRun(t, ctx, infra, "ACC-XFEAT",
		domain.ReconciliationScopeAccount, domain.SettlementTypeFinal,
		periodStart, periodEnd, "e2e-cross-feature")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	snap := createSnapshot(t, ctx, infra, run.RunID, "ACC-XFEAT", "GBP",
		decimal.NewFromFloat(10000.00),
		decimal.NewFromFloat(10250.00),
		"position-keeping", nil)
	_ = snap

	// Detect variances
	variances, err := infra.detector.DetectVariances(ctx, run.RunID)
	require.NoError(t, err)
	assert.Len(t, variances, 1)

	// Phase 2: Value variances
	err = infra.valuator.ValueVariances(ctx, run.RunID)
	require.NoError(t, err)

	// Verify variance is now valued
	valuedVars, err := infra.varianceRepo.FindByRunID(ctx, run.RunID)
	require.NoError(t, err)
	require.Len(t, valuedVars, 1)
	assert.NotEqual(t, domain.VarianceStatusDetected, valuedVars[0].Status)

	// Phase 3: Complete and finalize
	run, err = infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	require.NoError(t, run.Complete(1))
	require.NoError(t, infra.runRepo.Update(ctx, run))

	infra.mockPublisher.reset()
	err = infra.finalizer.FinalizeSettlement(ctx, run.RunID)
	require.NoError(t, err)

	// Verify finalization events
	lockEvents := infra.mockPublisher.getEventsByTopic("reconciliation.position-lock-requested.v1")
	assert.Len(t, lockEvents, 1)

	// Phase 4: Balance assertion via gRPC
	infra.mockPKClient.setSummary("ACC-XFEAT", "GBP",
		decimal.NewFromFloat(10250.00),
		decimal.NewFromFloat(10250.00), // Balanced after correction
	)

	assertResp, err := infra.grpcClient.AssertBalance(ctx, &reconciliationv1.AssertBalanceRequest{
		AccountId:       "ACC-XFEAT",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: "10250",
		RunId:           run.RunID.String(),
	})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.AssertionStatus_ASSERTION_STATUS_PASSED, assertResp.Assertion.Status)

	// Verify final state of all entities
	finalRun, err := infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusFinalized, finalRun.Status)
}

// TestCrossFeature_MultiInstrumentReconciliation tests reconciliation across
// multiple instrument codes in a single run.
func TestCrossFeature_MultiInstrumentReconciliation(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	run := createSettlementRun(t, ctx, infra, "ACC-MULTI",
		domain.ReconciliationScopeAccount, domain.SettlementTypeDaily,
		periodStart, periodEnd, "e2e-multi-instrument")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Create snapshots for 3 instruments with different variance patterns
	instruments := []struct {
		code     string
		expected float64
		actual   float64
	}{
		{"GBP", 1000.00, 1050.00}, // Variance: +50
		{"EUR", 5000.00, 5000.00}, // No variance
		{"USD", 2000.00, 1980.00}, // Variance: -20
	}

	for _, inst := range instruments {
		createSnapshot(t, ctx, infra, run.RunID, "ACC-MULTI", inst.code,
			decimal.NewFromFloat(inst.expected),
			decimal.NewFromFloat(inst.actual),
			"position-keeping", nil)
	}

	// Detect variances
	variances, err := infra.detector.DetectVariances(ctx, run.RunID)
	require.NoError(t, err)
	assert.Len(t, variances, 2, "should detect variances for GBP and USD only")

	// Value variances
	err = infra.valuator.ValueVariances(ctx, run.RunID)
	require.NoError(t, err)

	// Verify all variances are valued
	allVars, err := infra.varianceRepo.FindByRunID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Len(t, allVars, 2)
	for _, v := range allVars {
		assert.NotEqual(t, domain.VarianceStatusDetected, v.Status,
			"all variances should be valued for %s", v.InstrumentCode)
	}
}
