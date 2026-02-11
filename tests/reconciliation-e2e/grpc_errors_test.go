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
// gRPC Error Code Tests
// =============================================================================

// TestGRPCError_RetrieveNotFound verifies NotFound for a non-existent run.
func TestGRPCError_RetrieveNotFound(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	_, err := infra.grpcClient.RetrieveAccountReconciliation(ctx,
		&reconciliationv1.RetrieveAccountReconciliationRequest{
			RunId: uuid.New().String(),
		})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "error should be a gRPC status")
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestGRPCError_InvalidRunID verifies InvalidArgument for a malformed UUID.
func TestGRPCError_InvalidRunID(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	_, err := infra.grpcClient.RetrieveAccountReconciliation(ctx,
		&reconciliationv1.RetrieveAccountReconciliationRequest{
			RunId: "not-a-uuid",
		})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestGRPCError_InitiateMissingFields verifies InvalidArgument for missing required fields.
func TestGRPCError_InitiateMissingFields(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	tests := []struct {
		name string
		req  *reconciliationv1.InitiateAccountReconciliationRequest
	}{
		{
			name: "missing account_id",
			req: &reconciliationv1.InitiateAccountReconciliationRequest{
				Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
				SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
				PeriodStart:    timestamppb.New(periodStart),
				PeriodEnd:      timestamppb.New(periodEnd),
				InitiatedBy:    "test",
			},
		},
		{
			name: "unspecified scope",
			req: &reconciliationv1.InitiateAccountReconciliationRequest{
				AccountId:      "ACC-001",
				Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_UNSPECIFIED,
				SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
				PeriodStart:    timestamppb.New(periodStart),
				PeriodEnd:      timestamppb.New(periodEnd),
				InitiatedBy:    "test",
			},
		},
		{
			name: "unspecified settlement_type",
			req: &reconciliationv1.InitiateAccountReconciliationRequest{
				AccountId:      "ACC-001",
				Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
				SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_UNSPECIFIED,
				PeriodStart:    timestamppb.New(periodStart),
				PeriodEnd:      timestamppb.New(periodEnd),
				InitiatedBy:    "test",
			},
		},
		{
			name: "missing period_start",
			req: &reconciliationv1.InitiateAccountReconciliationRequest{
				AccountId:      "ACC-001",
				Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
				SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
				PeriodEnd:      timestamppb.New(periodEnd),
				InitiatedBy:    "test",
			},
		},
		{
			name: "missing initiated_by",
			req: &reconciliationv1.InitiateAccountReconciliationRequest{
				AccountId:      "ACC-001",
				Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
				SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
				PeriodStart:    timestamppb.New(periodStart),
				PeriodEnd:      timestamppb.New(periodEnd),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := infra.grpcClient.InitiateAccountReconciliation(ctx, tc.req)
			require.Error(t, err)
			st, ok := status.FromError(err)
			require.True(t, ok, "error should be a gRPC status")
			assert.Equal(t, codes.InvalidArgument, st.Code(),
				"expected InvalidArgument for %s, got %s: %s", tc.name, st.Code(), st.Message())
		})
	}
}

// TestGRPCError_ExecuteNotFound verifies NotFound for executing a non-existent run.
func TestGRPCError_ExecuteNotFound(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	_, err := infra.grpcClient.ExecuteAccountReconciliation(ctx,
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: uuid.New().String(),
		})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

// TestGRPCError_ExecuteWrongState verifies FailedPrecondition when executing a non-PENDING run.
func TestGRPCError_ExecuteWrongState(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	// Create and cancel a run so it's not in PENDING state
	initiateResp, err := infra.grpcClient.InitiateAccountReconciliation(ctx,
		&reconciliationv1.InitiateAccountReconciliationRequest{
			AccountId:      "ACC-WRONGSTATE",
			Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
			SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
			PeriodStart:    timestamppb.New(periodStart),
			PeriodEnd:      timestamppb.New(periodEnd),
			InitiatedBy:    "test",
		})
	require.NoError(t, err)

	// Cancel it
	_, err = infra.grpcClient.ControlAccountReconciliation(ctx,
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  initiateResp.Run.RunId,
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
		})
	require.NoError(t, err)

	// Try to execute the cancelled run
	_, err = infra.grpcClient.ExecuteAccountReconciliation(ctx,
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: initiateResp.Run.RunId,
		})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// TestGRPCError_ControlCancelledRunAgain verifies FailedPrecondition on double cancel.
func TestGRPCError_ControlCancelledRunAgain(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	initiateResp, err := infra.grpcClient.InitiateAccountReconciliation(ctx,
		&reconciliationv1.InitiateAccountReconciliationRequest{
			AccountId:      "ACC-DBLCANCEL",
			Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
			SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
			PeriodStart:    timestamppb.New(periodStart),
			PeriodEnd:      timestamppb.New(periodEnd),
			InitiatedBy:    "test",
		})
	require.NoError(t, err)

	// First cancel succeeds
	_, err = infra.grpcClient.ControlAccountReconciliation(ctx,
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  initiateResp.Run.RunId,
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
		})
	require.NoError(t, err)

	// Second cancel should fail
	_, err = infra.grpcClient.ControlAccountReconciliation(ctx,
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  initiateResp.Run.RunId,
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
		})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

// TestGRPCError_DisputeNotFoundVariance verifies error when disputing a non-existent variance.
func TestGRPCError_DisputeNotFoundVariance(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := contextWithAdminClaims(infra.tenantCtx())

	_, err := infra.grpcClient.InitiateDispute(ctx, &reconciliationv1.InitiateDisputeRequest{
		VarianceId: uuid.New().String(),
		RunId:      uuid.New().String(),
		AccountId:  "ACC-NF",
		Reason:     "Test dispute",
		RaisedBy:   "admin",
	})
	require.Error(t, err, "should fail for non-existent variance")
}

// TestGRPCError_RetrieveDisputeNotFound verifies NotFound for a non-existent dispute.
func TestGRPCError_RetrieveDisputeNotFound(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := contextWithAdminClaims(infra.tenantCtx())

	_, err := infra.grpcClient.RetrieveDispute(ctx, &reconciliationv1.RetrieveDisputeRequest{
		DisputeId: uuid.New().String(),
	})
	require.Error(t, err, "should fail for non-existent dispute")
}

// TestGRPCError_ListVariancesInvalidPageToken verifies InvalidArgument for bad page tokens.
func TestGRPCError_ListVariancesInvalidPageToken(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()

	_, err := infra.grpcClient.ListReconciliationResults(ctx,
		&reconciliationv1.ListReconciliationResultsRequest{
			RunId:     uuid.New().String(),
			PageToken: "not-valid-base64!!!",
		})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// =============================================================================
// Proto Enum Round-Trip Tests
// =============================================================================

// TestEnumRoundTrip_ReconciliationScope verifies ReconciliationScope round-trips through gRPC.
func TestEnumRoundTrip_ReconciliationScope(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	scopes := []reconciliationv1.ReconciliationScope{
		reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
		reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_INSTRUMENT,
		reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_PORTFOLIO,
		reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_FULL,
	}

	for _, scope := range scopes {
		t.Run(scope.String(), func(t *testing.T) {
			resp, err := infra.grpcClient.InitiateAccountReconciliation(ctx,
				&reconciliationv1.InitiateAccountReconciliationRequest{
					AccountId:      "ACC-SCOPE-" + scope.String(),
					Scope:          scope,
					SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
					PeriodStart:    timestamppb.New(periodStart),
					PeriodEnd:      timestamppb.New(periodEnd),
					InitiatedBy:    "e2e-enum-test",
				})
			require.NoError(t, err)
			assert.Equal(t, scope, resp.Run.Scope, "scope should round-trip through gRPC")
		})
	}
}

// TestEnumRoundTrip_SettlementType verifies SettlementType round-trips through gRPC.
func TestEnumRoundTrip_SettlementType(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	types := []reconciliationv1.SettlementType{
		reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
		reconciliationv1.SettlementType_SETTLEMENT_TYPE_WEEKLY,
		reconciliationv1.SettlementType_SETTLEMENT_TYPE_MONTHLY,
		reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND,
		reconciliationv1.SettlementType_SETTLEMENT_TYPE_END_OF_DAY,
		reconciliationv1.SettlementType_SETTLEMENT_TYPE_REAL_TIME,
	}

	for _, st := range types {
		t.Run(st.String(), func(t *testing.T) {
			resp, err := infra.grpcClient.InitiateAccountReconciliation(ctx,
				&reconciliationv1.InitiateAccountReconciliationRequest{
					AccountId:      "ACC-TYPE-" + st.String(),
					Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
					SettlementType: st,
					PeriodStart:    timestamppb.New(periodStart),
					PeriodEnd:      timestamppb.New(periodEnd),
					InitiatedBy:    "e2e-enum-test",
				})
			require.NoError(t, err)
			assert.Equal(t, st, resp.Run.SettlementType, "settlement type should round-trip through gRPC")
		})
	}
}

// TestEnumRoundTrip_RunStatus verifies RunStatus transitions are visible through gRPC.
func TestEnumRoundTrip_RunStatus(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	// PENDING
	resp, err := infra.grpcClient.InitiateAccountReconciliation(ctx,
		&reconciliationv1.InitiateAccountReconciliationRequest{
			AccountId:      "ACC-STATUS",
			Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
			SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
			PeriodStart:    timestamppb.New(periodStart),
			PeriodEnd:      timestamppb.New(periodEnd),
			InitiatedBy:    "e2e-status-test",
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_PENDING, resp.Run.Status)

	// CANCELLED
	cancelResp, err := infra.grpcClient.ControlAccountReconciliation(ctx,
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  resp.Run.RunId,
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_CANCELLED, cancelResp.Run.Status)
}

// TestEnumRoundTrip_VarianceStatusAndReason verifies variance enums round-trip through list results.
func TestEnumRoundTrip_VarianceStatusAndReason(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	run := createSettlementRun(t, ctx, infra, "ACC-VENUM",
		domain.ReconciliationScopeAccount, domain.SettlementTypeDaily,
		periodStart, periodEnd, "e2e-enum-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Create variances with different reasons
	reasons := []domain.VarianceReason{
		domain.VarianceReasonAmountMismatch,
		domain.VarianceReasonMissingEntry,
		domain.VarianceReasonDuplicateEntry,
		domain.VarianceReasonTimingDifference,
		domain.VarianceReasonCurrencyMismatch,
		domain.VarianceReasonDirectionError,
		domain.VarianceReasonOther,
	}

	expectedProtoReasons := []reconciliationv1.VarianceReason{
		reconciliationv1.VarianceReason_VARIANCE_REASON_AMOUNT_MISMATCH,
		reconciliationv1.VarianceReason_VARIANCE_REASON_MISSING_ENTRY,
		reconciliationv1.VarianceReason_VARIANCE_REASON_DUPLICATE_ENTRY,
		reconciliationv1.VarianceReason_VARIANCE_REASON_TIMING_DIFFERENCE,
		reconciliationv1.VarianceReason_VARIANCE_REASON_CURRENCY_MISMATCH,
		reconciliationv1.VarianceReason_VARIANCE_REASON_DIRECTION_ERROR,
		reconciliationv1.VarianceReason_VARIANCE_REASON_OTHER,
	}

	for _, reason := range reasons {
		snap := createSnapshot(t, ctx, infra, run.RunID, "ACC-VENUM", "GBP",
			decimal.NewFromFloat(100.00),
			decimal.NewFromFloat(110.00),
			"position-keeping", nil)
		createVariance(t, ctx, infra, run.RunID, snap.SnapshotID,
			"ACC-VENUM", "GBP",
			decimal.NewFromFloat(100.00),
			decimal.NewFromFloat(110.00),
			reason)
	}

	// List all variances via gRPC
	listResp, err := infra.grpcClient.ListReconciliationResults(ctx,
		&reconciliationv1.ListReconciliationResultsRequest{
			RunId:    run.RunID.String(),
			PageSize: 100,
		})
	require.NoError(t, err)
	require.Len(t, listResp.Variances, len(reasons))

	// Collect actual reasons returned
	actualReasons := make(map[reconciliationv1.VarianceReason]bool)
	for _, v := range listResp.Variances {
		actualReasons[v.Reason] = true
		// All should be OPEN status (DETECTED maps to OPEN in proto)
		assert.Equal(t, reconciliationv1.VarianceStatus_VARIANCE_STATUS_OPEN, v.Status,
			"DETECTED domain status should map to OPEN proto status")
	}

	for _, expected := range expectedProtoReasons {
		assert.True(t, actualReasons[expected],
			"expected proto reason %s to be present in results", expected)
	}
}

// TestEnumRoundTrip_DisputeStatus verifies dispute status transitions through gRPC.
func TestEnumRoundTrip_DisputeStatus(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := contextWithAdminClaims(infra.tenantCtx())
	periodStart, periodEnd := defaultPeriod()

	// Set up prerequisite data
	run := createSettlementRun(t, ctx, infra, "ACC-DENUM",
		domain.ReconciliationScopeAccount, domain.SettlementTypeDaily,
		periodStart, periodEnd, "e2e-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	snap := createSnapshot(t, ctx, infra, run.RunID, "ACC-DENUM", "GBP",
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(200.00),
		"position-keeping", nil)

	variance := createVariance(t, ctx, infra, run.RunID, snap.SnapshotID,
		"ACC-DENUM", "GBP",
		decimal.NewFromFloat(100.00),
		decimal.NewFromFloat(200.00),
		domain.VarianceReasonAmountMismatch)

	// OPEN
	initiateResp, err := infra.grpcClient.InitiateDispute(ctx,
		&reconciliationv1.InitiateDisputeRequest{
			VarianceId: variance.VarianceID.String(),
			RunId:      run.RunID.String(),
			AccountId:  "ACC-DENUM",
			Reason:     "Enum round-trip test",
			RaisedBy:   "e2e-admin",
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_OPEN, initiateResp.Dispute.Status)

	// ESCALATED
	escalateResp, err := infra.grpcClient.ControlDispute(ctx,
		&reconciliationv1.ControlDisputeRequest{
			DisputeId: initiateResp.Dispute.DisputeId,
			Action:    reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_ESCALATE,
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_ESCALATED, escalateResp.Dispute.Status)

	// RESOLVED
	resolveResp, err := infra.grpcClient.ControlDispute(ctx,
		&reconciliationv1.ControlDisputeRequest{
			DisputeId:  initiateResp.Dispute.DisputeId,
			Action:     reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_RESOLVE,
			Resolution: "Resolved for enum test",
			ResolvedBy: "e2e-admin",
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED, resolveResp.Dispute.Status)

	// Verify final state via Retrieve
	retrieveResp, err := infra.grpcClient.RetrieveDispute(ctx,
		&reconciliationv1.RetrieveDisputeRequest{
			DisputeId: initiateResp.Dispute.DisputeId,
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED, retrieveResp.Dispute.Status)
	assert.NotNil(t, retrieveResp.Dispute.ResolvedAt)
}
