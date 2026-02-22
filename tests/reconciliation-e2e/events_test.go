//go:build integration
// +build integration

package reconciliatione2e

import (
	"testing"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvents_SettlementFinalizedPublishesLockEvent verifies that finalizing a
// settlement publishes a PositionLockRequestedEvent.
func TestEvents_SettlementFinalizedPublishesLockEvent(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := contextWithServiceClaims(infra.tenantCtx())

	// Create, complete, and finalize a run
	run := createSettlementRun(t, ctx, infra, "ACC-EVT-1", domain.ReconciliationScopeAccount, domain.SettlementTypeFinal, periodStart, periodEnd, "e2e-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))
	run, _ = infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, run.Complete(0))
	require.NoError(t, infra.runRepo.Update(ctx, run))

	infra.mockPublisher.reset()

	err := infra.finalizer.FinalizeSettlement(ctx, run.RunID)
	require.NoError(t, err)

	// Verify lock event
	lockEvents := infra.mockPublisher.getEventsByTopic("reconciliation.position-lock-requested.v1")
	require.Len(t, lockEvents, 1)

	event, ok := lockEvents[0].Event.(service.PositionLockRequestedEvent)
	require.True(t, ok, "event should be PositionLockRequestedEvent")
	assert.Equal(t, run.RunID.String(), event.RunID)
	assert.Equal(t, "ACC-EVT-1", event.AccountID)
	assert.Equal(t, "LOCKED", event.Status)
}

// TestEvents_DisputeCreatedAndResolved verifies the full event trail for dispute lifecycle.
func TestEvents_DisputeCreatedAndResolved(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := contextWithAdminClaims(infra.tenantCtx())

	// Setup: run + variance
	run := createSettlementRun(t, ctx, infra, "ACC-EVT-2", domain.ReconciliationScopeAccount, domain.SettlementTypeDaily, periodStart, periodEnd, "e2e-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	snap := createSnapshot(t, ctx, infra, run.RunID, "ACC-EVT-2", "GBP",
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(200.00),
		"position-keeping", nil)

	variance := createVariance(t, ctx, infra, run.RunID, snap.SnapshotID,
		"ACC-EVT-2", "GBP",
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(200.00),
		domain.VarianceReasonAmountMismatch)

	infra.mockPublisher.reset()

	// Create dispute
	initiateResp, err := infra.grpcClient.InitiateDispute(ctx, &reconciliationv1.InitiateDisputeRequest{
		VarianceId: variance.VarianceID.String(),
		RunId:      run.RunID.String(),
		AccountId:  "ACC-EVT-2",
		Reason:     "Event test dispute",
		RaisedBy:   "e2e-admin",
	})
	require.NoError(t, err)

	// Verify created event
	createdEvents := infra.mockPublisher.getEventsByTopic("reconciliation.dispute-created.v1")
	require.Len(t, createdEvents, 1)
	createdEvent, ok := createdEvents[0].Event.(service.DisputeCreatedEvent)
	require.True(t, ok)
	assert.Equal(t, "ACC-EVT-2", createdEvent.AccountID)
	assert.Equal(t, "Event test dispute", createdEvent.Reason)

	// Resolve dispute
	_, err = infra.grpcClient.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{
		DisputeId:  initiateResp.Dispute.DisputeId,
		Action:     reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_RESOLVE,
		Resolution: "Fixed",
		ResolvedBy: "e2e-admin",
	})
	require.NoError(t, err)

	// Verify resolved event
	resolvedEvents := infra.mockPublisher.getEventsByTopic("reconciliation.dispute-resolved.v1")
	require.Len(t, resolvedEvents, 1)
	resolvedEvent, ok := resolvedEvents[0].Event.(service.DisputeResolvedEvent)
	require.True(t, ok)
	assert.Equal(t, "ACC-EVT-2", resolvedEvent.AccountID)
	assert.Equal(t, "Fixed", resolvedEvent.Resolution)
}

// TestEvents_BalanceImbalancePublishesEvent verifies that an imbalanced
// assertion publishes a BalanceImbalanceDetectedEvent.
func TestEvents_BalanceImbalancePublishesEvent(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	infra.mockPKClient.setSummary("ACC-EVT-3", "GBP",
		decimal.NewFromFloat(10000.00),
		decimal.NewFromFloat(9500.00), // 500 imbalance
	)

	infra.mockPublisher.reset()

	result, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
		AccountID:       "ACC-EVT-3",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.NewFromFloat(10000.00),
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      service.CallerRoleTenantAdmin,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusFailed, result.Assertion.Status)

	// Verify imbalance event
	imbalanceEvents := infra.mockPublisher.getEventsByTopic("reconciliation.balance-imbalance-detected.v1")
	require.Len(t, imbalanceEvents, 1)

	event, ok := imbalanceEvents[0].Event.(*domain.BalanceImbalanceDetectedEvent)
	require.True(t, ok, "event should be *BalanceImbalanceDetectedEvent")
	assert.Equal(t, "GBP", event.InstrumentCode)
	assert.True(t, event.TotalDebits.Equal(decimal.NewFromFloat(10000.00)))
	assert.True(t, event.TotalCredits.Equal(decimal.NewFromFloat(9500.00)))
	assert.True(t, event.ImbalanceAmount.Equal(decimal.NewFromFloat(500.00)))
}

// TestEvents_SnapshotCaptureDoesNotPublishOnSuccess verifies that successful
// snapshot capture does not publish events (events are only for finalization).
func TestEvents_SnapshotCaptureDoesNotPublishOnSuccess(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := infra.tenantCtx()

	run := createSettlementRun(t, ctx, infra, "ACC-EVT-4", domain.ReconciliationScopeAccount, domain.SettlementTypeDaily, periodStart, periodEnd, "e2e-test")

	infra.mockPKProvider.setPages([]service.PositionPage{
		{
			Records: []service.PositionRecord{
				{
					AccountID:      "ACC-EVT-4",
					InstrumentCode: "GBP",
					Balance:        decimal.NewFromFloat(100.00),
					SourceSystem:   "position-keeping",
				},
			},
		},
	})

	infra.mockPublisher.reset()

	err := infra.capturer.CaptureSnapshots(ctx, run.RunID)
	require.NoError(t, err)

	// No events should be published during capture
	allEvents := infra.mockPublisher.getEvents()
	assert.Empty(t, allEvents, "snapshot capture should not publish events")
}

// TestCrossService_EndToEndWithDisputeAndAssertion tests a scenario that
// spans settlement, dispute, and balance assertion workflows.
func TestCrossService_EndToEndWithDisputeAndAssertion(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := contextWithAdminClaims(
		contextWithServiceClaims(infra.tenantCtx()),
	)

	// Phase 1: Create settlement run with variances
	run := createSettlementRun(t, ctx, infra, "ACC-XSVC", domain.ReconciliationScopeAccount, domain.SettlementTypeFinal, periodStart, periodEnd, "e2e-integration")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	snap := createSnapshot(t, ctx, infra, run.RunID, "ACC-XSVC", "GBP",
		decimal.NewFromFloat(10000.00),
		decimal.NewFromFloat(10500.00),
		"position-keeping", nil)

	variance := createVariance(t, ctx, infra, run.RunID, snap.SnapshotID,
		"ACC-XSVC", "GBP",
		decimal.NewFromFloat(10000.00),
		decimal.NewFromFloat(10500.00),
		domain.VarianceReasonAmountMismatch)

	// Value the variance
	err := infra.valuator.ValueVariances(ctx, run.RunID)
	require.NoError(t, err)

	// Phase 2: Raise and resolve a dispute on the variance
	initiateResp, err := infra.grpcClient.InitiateDispute(ctx, &reconciliationv1.InitiateDisputeRequest{
		VarianceId: variance.VarianceID.String(),
		RunId:      run.RunID.String(),
		AccountId:  "ACC-XSVC",
		Reason:     "Booking error in counterparty system",
		RaisedBy:   "e2e-admin",
	})
	require.NoError(t, err)

	_, err = infra.grpcClient.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{
		DisputeId:  initiateResp.Dispute.DisputeId,
		Action:     reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_RESOLVE,
		Resolution: "Counterparty confirmed correction",
		ResolvedBy: "e2e-admin",
	})
	require.NoError(t, err)

	// Phase 3: Complete and finalize the run
	run, _ = infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, run.Complete(1))
	require.NoError(t, infra.runRepo.Update(ctx, run))

	err = infra.finalizer.FinalizeSettlement(ctx, run.RunID)
	require.NoError(t, err)

	// Phase 4: Run balance assertion
	infra.mockPKClient.setSummary("ACC-XSVC", "GBP",
		decimal.NewFromFloat(10500.00),
		decimal.NewFromFloat(10500.00), // Balanced after correction
	)

	result, err := infra.assertor.ExecuteBalanceAssertion(ctx, service.AssertBalanceRequest{
		AccountID:       "ACC-XSVC",
		InstrumentCode:  "GBP",
		Expression:      "total_debits == total_credits",
		ExpectedBalance: decimal.NewFromFloat(10500.00),
		RunID:           &run.RunID,
		Scope:           domain.AssertionScopePositionLedger,
		CallerRole:      service.CallerRoleTenantAdmin,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.AssertionStatusPassed, result.Assertion.Status)

	// Verify the full event trail
	allEvents := infra.mockPublisher.getEvents()
	assert.NotEmpty(t, allEvents, "should have events from dispute + finalization + assertion")

	// Verify final state of all entities
	finalRun, err := infra.runRepo.FindByID(ctx, run.RunID)
	require.NoError(t, err)
	assert.Equal(t, domain.RunStatusFinalized, finalRun.Status)

	disputeID, _ := uuid.Parse(initiateResp.Dispute.DisputeId)
	finalDispute, err := infra.disputeRepo.FindByID(ctx, disputeID)
	require.NoError(t, err)
	assert.Equal(t, domain.DisputeStatusResolved, finalDispute.Status)
}
