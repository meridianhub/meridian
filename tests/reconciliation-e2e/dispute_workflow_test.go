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
)

// TestDisputeWorkflow_FullLifecycle tests the dispute lifecycle:
// Create dispute -> review -> escalate -> resolve, verifying state transitions.
func TestDisputeWorkflow_FullLifecycle(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := contextWithAdminClaims(infra.tenantCtx())

	// Create prerequisite data: run + variance
	run := createSettlementRun(t, ctx, infra, "ACC-DISP", domain.ReconciliationScopeAccount, domain.SettlementTypeDaily, periodStart, periodEnd, "e2e-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	snap := createSnapshot(t, ctx, infra, run.RunID, "ACC-DISP", "GBP",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(1100.00),
		"position-keeping", nil)

	variance := createVariance(t, ctx, infra, run.RunID, snap.SnapshotID,
		"ACC-DISP", "GBP",
		decimal.NewFromFloat(1000.00),
		decimal.NewFromFloat(1100.00),
		domain.VarianceReasonAmountMismatch)

	// Step 1: Initiate dispute via gRPC service
	initiateResp, err := infra.grpcService.InitiateDispute(ctx, &reconciliationv1.InitiateDisputeRequest{
		VarianceId: variance.VarianceID.String(),
		RunId:      run.RunID.String(),
		AccountId:  "ACC-DISP",
		Reason:     "Incorrect booking amount",
		RaisedBy:   "e2e-admin",
	})
	require.NoError(t, err)
	require.NotNil(t, initiateResp)

	disputeID := initiateResp.Dispute.DisputeId
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_OPEN, initiateResp.Dispute.Status)
	assert.Equal(t, "Incorrect booking amount", initiateResp.Dispute.Reason)

	// Verify dispute created event was published
	createdEvents := infra.mockPublisher.getEventsByTopic("reconciliation.dispute.created")
	assert.Len(t, createdEvents, 1)

	// Step 2: Escalate the dispute
	escalateResp, err := infra.grpcService.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{
		DisputeId: disputeID,
		Action:    reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_ESCALATE,
	})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_ESCALATED, escalateResp.Dispute.Status)

	// Step 3: Resolve the dispute (requires admin or operator role)
	resolveResp, err := infra.grpcService.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{
		DisputeId:  disputeID,
		Action:     reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_RESOLVE,
		Resolution: "Amount corrected after investigation",
		ResolvedBy: "e2e-admin",
	})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED, resolveResp.Dispute.Status)
	assert.Equal(t, "Amount corrected after investigation", resolveResp.Dispute.Resolution)
	assert.NotNil(t, resolveResp.Dispute.ResolvedAt)

	// Verify dispute resolved event was published
	resolvedEvents := infra.mockPublisher.getEventsByTopic("reconciliation.dispute.resolved")
	assert.Len(t, resolvedEvents, 1)

	// Verify saga was invoked for resolution
	sagaCalls := infra.mockSagaRT.getCalls()
	assert.Len(t, sagaCalls, 1)
	assert.Equal(t, "reconciliation_adjustment", sagaCalls[0].Name)

	// Step 4: Retrieve the dispute to verify final state
	retrieveResp, err := infra.grpcService.RetrieveDispute(ctx, &reconciliationv1.RetrieveDisputeRequest{
		DisputeId: disputeID,
	})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED, retrieveResp.Dispute.Status)
}

// TestDisputeWorkflow_Rejection tests the dispute rejection path.
func TestDisputeWorkflow_Rejection(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := contextWithAdminClaims(infra.tenantCtx())

	// Create prerequisite data
	run := createSettlementRun(t, ctx, infra, "ACC-REJ", domain.ReconciliationScopeAccount, domain.SettlementTypeDaily, periodStart, periodEnd, "e2e-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	snap := createSnapshot(t, ctx, infra, run.RunID, "ACC-REJ", "GBP",
		decimal.NewFromFloat(500.00),
		decimal.NewFromFloat(550.00),
		"position-keeping", nil)

	variance := createVariance(t, ctx, infra, run.RunID, snap.SnapshotID,
		"ACC-REJ", "GBP",
		decimal.NewFromFloat(500.00),
		decimal.NewFromFloat(550.00),
		domain.VarianceReasonAmountMismatch)

	// Initiate dispute
	initiateResp, err := infra.grpcService.InitiateDispute(ctx, &reconciliationv1.InitiateDisputeRequest{
		VarianceId: variance.VarianceID.String(),
		RunId:      run.RunID.String(),
		AccountId:  "ACC-REJ",
		Reason:     "Disputed amount",
		RaisedBy:   "e2e-admin",
	})
	require.NoError(t, err)

	// Reject the dispute
	rejectResp, err := infra.grpcService.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{
		DisputeId:  initiateResp.Dispute.DisputeId,
		Action:     reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_REJECT,
		Resolution: "Variance confirmed as correct",
		ResolvedBy: "e2e-admin",
	})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_REJECTED, rejectResp.Dispute.Status)

	// Verify no saga was invoked for rejection
	sagaCalls := infra.mockSagaRT.getCalls()
	assert.Empty(t, sagaCalls, "saga should not be invoked for rejected disputes")
}

// TestDisputeWorkflow_InvalidTransitions verifies that invalid state transitions are rejected.
func TestDisputeWorkflow_InvalidTransitions(t *testing.T) {
	infra := setupE2EInfra(t)
	periodStart, periodEnd := defaultPeriod()
	ctx := contextWithAdminClaims(infra.tenantCtx())

	// Create prerequisite data
	run := createSettlementRun(t, ctx, infra, "ACC-INV", domain.ReconciliationScopeAccount, domain.SettlementTypeDaily, periodStart, periodEnd, "e2e-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	snap := createSnapshot(t, ctx, infra, run.RunID, "ACC-INV", "GBP",
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(200.00),
		"position-keeping", nil)

	variance := createVariance(t, ctx, infra, run.RunID, snap.SnapshotID,
		"ACC-INV", "GBP",
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(200.00),
		domain.VarianceReasonAmountMismatch)

	// Create and resolve a dispute
	initiateResp, err := infra.grpcService.InitiateDispute(ctx, &reconciliationv1.InitiateDisputeRequest{
		VarianceId: variance.VarianceID.String(),
		RunId:      run.RunID.String(),
		AccountId:  "ACC-INV",
		Reason:     "Test dispute",
		RaisedBy:   "e2e-admin",
	})
	require.NoError(t, err)

	// Resolve it
	_, err = infra.grpcService.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{
		DisputeId:  initiateResp.Dispute.DisputeId,
		Action:     reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_RESOLVE,
		Resolution: "Resolved",
		ResolvedBy: "admin",
	})
	require.NoError(t, err)

	// Try to escalate a resolved dispute - should fail
	_, err = infra.grpcService.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{
		DisputeId: initiateResp.Dispute.DisputeId,
		Action:    reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_ESCALATE,
	})
	require.Error(t, err, "should not be able to escalate a resolved dispute")
}

// TestDisputeWorkflow_NotFoundVariance verifies that initiating a dispute
// against a non-existent variance returns NOT_FOUND.
func TestDisputeWorkflow_NotFoundVariance(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := contextWithAdminClaims(infra.tenantCtx())

	_, err := infra.grpcService.InitiateDispute(ctx, &reconciliationv1.InitiateDisputeRequest{
		VarianceId: uuid.New().String(),
		RunId:      uuid.New().String(),
		AccountId:  "ACC-NF",
		Reason:     "Test",
		RaisedBy:   "admin",
	})
	require.Error(t, err, "should fail for non-existent variance")
}

// TestDisputeWorkflow_RetrieveNotFound verifies retrieving a non-existent dispute.
func TestDisputeWorkflow_RetrieveNotFound(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := contextWithAdminClaims(infra.tenantCtx())

	_, err := infra.grpcService.RetrieveDispute(ctx, &reconciliationv1.RetrieveDisputeRequest{
		DisputeId: uuid.New().String(),
	})
	require.Error(t, err, "should fail for non-existent dispute")
}
