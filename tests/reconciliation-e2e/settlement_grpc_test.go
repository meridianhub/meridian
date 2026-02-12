//go:build integration
// +build integration

package reconciliatione2e

import (
	"testing"
	"time"

	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestSettlementGRPC_FullLifecycle exercises the full settlement lifecycle via gRPC:
// Initiate -> Execute -> poll Retrieve until COMPLETED -> List variances.
func TestSettlementGRPC_FullLifecycle(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	// Set up mock position data that will produce a variance when captured.
	// First capture: establishes baseline snapshots (expected == actual).
	infra.mockPKProvider.setPages([]service.PositionPage{
		{
			Records: []service.PositionRecord{
				{
					AccountID:      "ACC-GRPC-001",
					InstrumentCode: "GBP",
					Balance:        decimal.NewFromFloat(1000.50),
					SourceSystem:   "position-keeping",
					Attributes:     map[string]string{"quality": "ACTUAL"},
				},
				{
					AccountID:      "ACC-GRPC-001",
					InstrumentCode: "EUR",
					Balance:        decimal.NewFromFloat(2500.00),
					SourceSystem:   "position-keeping",
					Attributes:     map[string]string{"quality": "ESTIMATE"},
				},
			},
		},
	})

	// Step 1: Initiate a settlement run via gRPC
	initiateResp, err := infra.grpcClient.InitiateAccountReconciliation(ctx,
		&reconciliationv1.InitiateAccountReconciliationRequest{
			AccountId:      "ACC-GRPC-001",
			Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
			SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY,
			PeriodStart:    timestamppb.New(periodStart),
			PeriodEnd:      timestamppb.New(periodEnd),
			InitiatedBy:    "e2e-grpc-test",
		})
	require.NoError(t, err)
	require.NotNil(t, initiateResp.Run)

	runID := initiateResp.Run.RunId
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_PENDING, initiateResp.Run.Status)
	assert.Equal(t, "ACC-GRPC-001", initiateResp.Run.AccountId)
	assert.Equal(t, reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT, initiateResp.Run.Scope)
	assert.Equal(t, reconciliationv1.SettlementType_SETTLEMENT_TYPE_DAILY, initiateResp.Run.SettlementType)

	// Step 2: Execute the run via gRPC (triggers async pipeline)
	executeResp, err := infra.grpcClient.ExecuteAccountReconciliation(ctx,
		&reconciliationv1.ExecuteAccountReconciliationRequest{
			RunId: runID,
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_RUNNING, executeResp.Run.Status)

	// Step 3: Poll Retrieve until COMPLETED using await
	err = await.New().
		AtMost(30 * time.Second).
		PollInterval(200 * time.Millisecond).
		Until(func() bool {
			resp, pollErr := infra.grpcClient.RetrieveAccountReconciliation(ctx,
				&reconciliationv1.RetrieveAccountReconciliationRequest{
					RunId: runID,
				})
			if pollErr != nil {
				return false
			}
			return resp.Run.Status == reconciliationv1.RunStatus_RUN_STATUS_COMPLETED
		})
	require.NoError(t, err, "settlement run should reach COMPLETED status")

	// Step 4: Retrieve final state and verify
	retrieveResp, err := infra.grpcClient.RetrieveAccountReconciliation(ctx,
		&reconciliationv1.RetrieveAccountReconciliationRequest{
			RunId: runID,
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_COMPLETED, retrieveResp.Run.Status)
	assert.Equal(t, "e2e-grpc-test", retrieveResp.Run.InitiatedBy)

	// Step 5: List variances for this run
	listResp, err := infra.grpcClient.ListReconciliationResults(ctx,
		&reconciliationv1.ListReconciliationResultsRequest{
			RunId:    runID,
			PageSize: 50,
		})
	require.NoError(t, err)
	// First run with expected == actual should produce zero variances
	assert.Empty(t, listResp.Variances, "first run should produce no variances when expected == actual")
}

// TestSettlementGRPC_CancelRun tests the cancel workflow via gRPC:
// Initiate -> Control(CANCEL) -> Retrieve -> verify CANCELLED.
func TestSettlementGRPC_CancelRun(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	// Initiate
	initiateResp, err := infra.grpcClient.InitiateAccountReconciliation(ctx,
		&reconciliationv1.InitiateAccountReconciliationRequest{
			AccountId:      "ACC-CANCEL",
			Scope:          reconciliationv1.ReconciliationScope_RECONCILIATION_SCOPE_ACCOUNT,
			SettlementType: reconciliationv1.SettlementType_SETTLEMENT_TYPE_ON_DEMAND,
			PeriodStart:    timestamppb.New(periodStart),
			PeriodEnd:      timestamppb.New(periodEnd),
			InitiatedBy:    "e2e-cancel-test",
		})
	require.NoError(t, err)
	runID := initiateResp.Run.RunId

	// Cancel the PENDING run
	controlResp, err := infra.grpcClient.ControlAccountReconciliation(ctx,
		&reconciliationv1.ControlAccountReconciliationRequest{
			RunId:  runID,
			Action: reconciliationv1.ControlAction_CONTROL_ACTION_CANCEL,
			Reason: "e2e test cancellation",
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_CANCELLED, controlResp.Run.Status)

	// Retrieve and verify CANCELLED
	retrieveResp, err := infra.grpcClient.RetrieveAccountReconciliation(ctx,
		&reconciliationv1.RetrieveAccountReconciliationRequest{
			RunId: runID,
		})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.RunStatus_RUN_STATUS_CANCELLED, retrieveResp.Run.Status)
}

// TestSettlementGRPC_ListVariancesWithPagination tests paginated variance listing via gRPC.
func TestSettlementGRPC_ListVariancesWithPagination(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	// Create a run with known variances directly in DB for pagination testing
	run := createSettlementRun(t, ctx, infra, "ACC-PAGE",
		domain.ReconciliationScopeAccount, domain.SettlementTypeDaily,
		periodStart, periodEnd, "e2e-pagination-test")
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	// Create 5 variances
	for i := 0; i < 5; i++ {
		snap := createSnapshot(t, ctx, infra, run.RunID, "ACC-PAGE", "GBP",
			decimal.NewFromFloat(float64(100+i)),
			decimal.NewFromFloat(float64(110+i)),
			"position-keeping", nil)

		createVariance(t, ctx, infra, run.RunID, snap.SnapshotID,
			"ACC-PAGE", "GBP",
			decimal.NewFromFloat(float64(100+i)),
			decimal.NewFromFloat(float64(110+i)),
			domain.VarianceReasonAmountMismatch)
	}

	// List with page size 2 - first page
	page1, err := infra.grpcClient.ListReconciliationResults(ctx,
		&reconciliationv1.ListReconciliationResultsRequest{
			RunId:    run.RunID.String(),
			PageSize: 2,
		})
	require.NoError(t, err)
	assert.Len(t, page1.Variances, 2)
	assert.NotEmpty(t, page1.NextPageToken, "should have next page token")

	// Second page
	page2, err := infra.grpcClient.ListReconciliationResults(ctx,
		&reconciliationv1.ListReconciliationResultsRequest{
			RunId:     run.RunID.String(),
			PageSize:  2,
			PageToken: page1.NextPageToken,
		})
	require.NoError(t, err)
	assert.Len(t, page2.Variances, 2)
	assert.NotEmpty(t, page2.NextPageToken, "should have next page token for page 3")

	// Third page (last)
	page3, err := infra.grpcClient.ListReconciliationResults(ctx,
		&reconciliationv1.ListReconciliationResultsRequest{
			RunId:     run.RunID.String(),
			PageSize:  2,
			PageToken: page2.NextPageToken,
		})
	require.NoError(t, err)
	assert.Len(t, page3.Variances, 1, "last page should have remaining 1 variance")
	assert.Empty(t, page3.NextPageToken, "last page should have no next page token")

	// Verify variance details round-trip through proto
	for _, v := range page1.Variances {
		assert.NotEmpty(t, v.VarianceId)
		assert.Equal(t, run.RunID.String(), v.RunId)
		assert.Equal(t, "ACC-PAGE", v.AccountId)
		assert.Equal(t, "GBP", v.InstrumentCode)
		assert.Equal(t, reconciliationv1.VarianceReason_VARIANCE_REASON_AMOUNT_MISMATCH, v.Reason)
		assert.Equal(t, reconciliationv1.VarianceStatus_VARIANCE_STATUS_OPEN, v.Status)
	}
}

// TestSettlementGRPC_ExecuteWithVariances tests the full pipeline that produces variances.
func TestSettlementGRPC_ExecuteWithVariances(t *testing.T) {
	infra := setupE2EInfra(t)
	ctx := infra.tenantCtx()
	periodStart, periodEnd := defaultPeriod()

	// Create a run directly so we have a known RunID for assertion queries
	run := createSettlementRun(t, ctx, infra, "ACC-VAR",
		domain.ReconciliationScopeAccount, domain.SettlementTypeDaily,
		periodStart, periodEnd, "e2e-variance-test")

	// Pre-create snapshots with variance-producing values
	// (since the mock PK provider only provides actual balances, and
	// the capturer sets expected == actual, we seed data to create variances
	// by creating snapshots manually and having the detector compare them.)
	require.NoError(t, run.Start())
	require.NoError(t, infra.runRepo.Update(ctx, run))

	createSnapshot(t, ctx, infra, run.RunID, "ACC-VAR", "GBP",
		decimal.NewFromFloat(1000.00), // expected
		decimal.NewFromFloat(1050.75), // actual
		"position-keeping", map[string]string{"quality": "ACTUAL"})

	// Detect variances using the service component directly (since Execute
	// runs the full pipeline and we've already seeded the data)
	variances, err := infra.detector.DetectVariances(ctx, run.RunID)
	require.NoError(t, err)
	assert.Len(t, variances, 1)

	// Now list the variance via gRPC
	listResp, err := infra.grpcClient.ListReconciliationResults(ctx,
		&reconciliationv1.ListReconciliationResultsRequest{
			RunId: run.RunID.String(),
		})
	require.NoError(t, err)
	require.Len(t, listResp.Variances, 1)

	v := listResp.Variances[0]
	assert.Equal(t, "ACC-VAR", v.AccountId)
	assert.Equal(t, "GBP", v.InstrumentCode)
	assert.Equal(t, "1000", v.ExpectedAmount)
	assert.Equal(t, "1050.75", v.ActualAmount)
	assert.Equal(t, reconciliationv1.VarianceReason_VARIANCE_REASON_AMOUNT_MISMATCH, v.Reason)
}
