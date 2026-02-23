package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockDisputeLister implements service.DisputeLister for testing.
type mockDisputeLister struct {
	disputes []*domain.Dispute
	err      error
}

func (m *mockDisputeLister) List(_ context.Context, filter domain.DisputeFilter) ([]*domain.Dispute, error) {
	if m.err != nil {
		return nil, m.err
	}
	result := make([]*domain.Dispute, 0, len(m.disputes))
	for _, d := range m.disputes {
		if filter.RunID != nil && d.RunID != *filter.RunID {
			continue
		}
		if filter.Status != nil && d.Status != *filter.Status {
			continue
		}
		result = append(result, d)
	}
	// Apply limit/offset
	if filter.Offset >= len(result) {
		return []*domain.Dispute{}, nil
	}
	result = result[filter.Offset:]
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

// mockAssertionLister implements service.AssertionLister for testing.
type mockAssertionLister struct {
	assertions []*domain.BalanceAssertion
	err        error
}

func (m *mockAssertionLister) List(_ context.Context, filter domain.AssertionFilter) ([]*domain.BalanceAssertion, error) {
	if m.err != nil {
		return nil, m.err
	}
	result := make([]*domain.BalanceAssertion, 0, len(m.assertions))
	for _, a := range m.assertions {
		if filter.RunID != nil && (a.RunID == nil || *a.RunID != *filter.RunID) {
			continue
		}
		result = append(result, a)
	}
	// Apply limit/offset
	if filter.Offset >= len(result) {
		return []*domain.BalanceAssertion{}, nil
	}
	result = result[filter.Offset:]
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	return result, nil
}

func makeDisputeForRun(t *testing.T, runID uuid.UUID, s domain.DisputeStatus) *domain.Dispute {
	t.Helper()
	now := time.Now().UTC()
	return &domain.Dispute{
		DisputeID:  uuid.New(),
		VarianceID: uuid.New(),
		RunID:      runID,
		AccountID:  "ACC-001",
		Status:     s,
		Reason:     "Test reason",
		RaisedBy:   "user-1",
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

func makeAssertionForRun(t *testing.T, runID uuid.UUID) *domain.BalanceAssertion {
	t.Helper()
	now := time.Now().UTC()
	return &domain.BalanceAssertion{
		AssertionID:     uuid.New(),
		RunID:           &runID,
		AccountID:       "ACC-001",
		InstrumentCode:  "GBP",
		Expression:      "balance >= 0",
		ExpectedBalance: decimal.NewFromFloat(1000.00),
		ActualBalance:   decimal.NewFromFloat(1000.00),
		Status:          domain.AssertionStatusPassed,
		CreatedAt:       now,
	}
}

func TestListDisputes_Success(t *testing.T) {
	runID := uuid.New()
	lister := &mockDisputeLister{
		disputes: []*domain.Dispute{
			makeDisputeForRun(t, runID, domain.DisputeStatusOpen),
			makeDisputeForRun(t, runID, domain.DisputeStatusResolved),
		},
	}
	svc := service.NewAccountReconciliationService(
		service.WithDisputeListRepository(lister),
	)

	resp, err := svc.ListDisputes(context.Background(), &reconciliationv1.ListDisputesRequest{
		RunId: runID.String(),
	})
	require.NoError(t, err)
	assert.Len(t, resp.Items, 2)
	assert.Empty(t, resp.NextPageToken)
	assert.Equal(t, int64(-1), resp.TotalCount)
}

func TestListDisputes_InvalidRunID(t *testing.T) {
	svc := service.NewAccountReconciliationService(
		service.WithDisputeListRepository(&mockDisputeLister{}),
	)

	_, err := svc.ListDisputes(context.Background(), &reconciliationv1.ListDisputesRequest{
		RunId: "not-a-uuid",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestListDisputes_NotImplemented(t *testing.T) {
	svc := service.NewAccountReconciliationService()

	_, err := svc.ListDisputes(context.Background(), &reconciliationv1.ListDisputesRequest{
		RunId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestListDisputes_WithStatusFilter(t *testing.T) {
	runID := uuid.New()
	lister := &mockDisputeLister{
		disputes: []*domain.Dispute{
			makeDisputeForRun(t, runID, domain.DisputeStatusOpen),
			makeDisputeForRun(t, runID, domain.DisputeStatusResolved),
			makeDisputeForRun(t, runID, domain.DisputeStatusOpen),
		},
	}
	svc := service.NewAccountReconciliationService(
		service.WithDisputeListRepository(lister),
	)

	resp, err := svc.ListDisputes(context.Background(), &reconciliationv1.ListDisputesRequest{
		RunId:        runID.String(),
		FilterStatus: reconciliationv1.DisputeStatus_DISPUTE_STATUS_OPEN,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Items, 2)
	for _, item := range resp.Items {
		assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_OPEN, item.Status)
	}
}

func TestListDisputes_Pagination(t *testing.T) {
	runID := uuid.New()
	disputes := make([]*domain.Dispute, 60)
	for i := range disputes {
		disputes[i] = makeDisputeForRun(t, runID, domain.DisputeStatusOpen)
	}
	lister := &mockDisputeLister{disputes: disputes}
	svc := service.NewAccountReconciliationService(
		service.WithDisputeListRepository(lister),
	)

	// First page
	resp, err := svc.ListDisputes(context.Background(), &reconciliationv1.ListDisputesRequest{
		RunId:    runID.String(),
		PageSize: 50,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Items, 50)
	assert.NotEmpty(t, resp.NextPageToken)

	// Second page
	resp2, err := svc.ListDisputes(context.Background(), &reconciliationv1.ListDisputesRequest{
		RunId:     runID.String(),
		PageSize:  50,
		PageToken: resp.NextPageToken,
	})
	require.NoError(t, err)
	assert.Len(t, resp2.Items, 10)
	assert.Empty(t, resp2.NextPageToken)
}

func TestListDisputes_InvalidPageToken(t *testing.T) {
	svc := service.NewAccountReconciliationService(
		service.WithDisputeListRepository(&mockDisputeLister{}),
	)

	_, err := svc.ListDisputes(context.Background(), &reconciliationv1.ListDisputesRequest{
		RunId:     uuid.New().String(),
		PageToken: "not-base64!!",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUpdateDispute_ResolveSuccess(t *testing.T) {
	runID := uuid.New()
	d := makeDisputeForRun(t, runID, domain.DisputeStatusOpen)

	repo := newMockDisputeRepo()
	repo.disputes[d.DisputeID] = d

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(repo),
	)

	resp, err := svc.UpdateDispute(context.Background(), &reconciliationv1.UpdateDisputeRequest{
		RunId:           runID.String(),
		DisputeId:       d.DisputeID.String(),
		Status:          reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED,
		ResolutionNotes: "Fixed by correcting entry",
		ResolvedBy:      "user@example.com",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED, resp.Dispute.Status)
	assert.Equal(t, "Fixed by correcting entry", resp.Dispute.Resolution)
	assert.Equal(t, "user@example.com", resp.Dispute.ResolvedBy)
}

func TestUpdateDispute_EscalateSuccess(t *testing.T) {
	runID := uuid.New()
	d := makeDisputeForRun(t, runID, domain.DisputeStatusOpen)
	require.NoError(t, d.Review())

	repo := newMockDisputeRepo()
	repo.disputes[d.DisputeID] = d

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(repo),
	)

	resp, err := svc.UpdateDispute(context.Background(), &reconciliationv1.UpdateDisputeRequest{
		RunId:     runID.String(),
		DisputeId: d.DisputeID.String(),
		Status:    reconciliationv1.DisputeStatus_DISPUTE_STATUS_ESCALATED,
	})
	require.NoError(t, err)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_ESCALATED, resp.Dispute.Status)
}

func TestUpdateDispute_NotFound(t *testing.T) {
	repo := newMockDisputeRepo()
	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(repo),
	)

	_, err := svc.UpdateDispute(context.Background(), &reconciliationv1.UpdateDisputeRequest{
		RunId:     uuid.New().String(),
		DisputeId: uuid.New().String(),
		Status:    reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUpdateDispute_InvalidTransition(t *testing.T) {
	runID := uuid.New()
	d := makeDisputeForRun(t, runID, domain.DisputeStatusOpen)
	require.NoError(t, d.Resolve("done", "admin"))

	repo := newMockDisputeRepo()
	repo.disputes[d.DisputeID] = d

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(repo),
	)

	_, err := svc.UpdateDispute(context.Background(), &reconciliationv1.UpdateDisputeRequest{
		RunId:     runID.String(),
		DisputeId: d.DisputeID.String(),
		Status:    reconciliationv1.DisputeStatus_DISPUTE_STATUS_REJECTED,
	})
	require.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestUpdateDispute_InvalidDisputeID(t *testing.T) {
	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(newMockDisputeRepo()),
	)

	_, err := svc.UpdateDispute(context.Background(), &reconciliationv1.UpdateDisputeRequest{
		RunId:     uuid.New().String(),
		DisputeId: "not-a-uuid",
		Status:    reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUpdateDispute_InvalidRunID(t *testing.T) {
	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(newMockDisputeRepo()),
	)

	_, err := svc.UpdateDispute(context.Background(), &reconciliationv1.UpdateDisputeRequest{
		RunId:     "not-a-uuid",
		DisputeId: uuid.New().String(),
		Status:    reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestUpdateDispute_RunIDMismatch(t *testing.T) {
	repo := newMockDisputeRepo()
	d := makeDisputeForRun(t, uuid.New(), domain.DisputeStatusOpen)
	repo.disputes[d.DisputeID] = d

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(repo),
	)

	// Use a different run_id than the dispute's run_id
	_, err := svc.UpdateDispute(context.Background(), &reconciliationv1.UpdateDisputeRequest{
		RunId:     uuid.New().String(), // different run
		DisputeId: d.DisputeID.String(),
		Status:    reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED,
	})
	require.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestUpdateDispute_UnspecifiedStatus(t *testing.T) {
	repo := newMockDisputeRepo()
	d := makeDisputeForRun(t, uuid.New(), domain.DisputeStatusOpen)
	repo.disputes[d.DisputeID] = d

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(repo),
	)

	_, err := svc.UpdateDispute(context.Background(), &reconciliationv1.UpdateDisputeRequest{
		RunId:     d.RunID.String(),
		DisputeId: d.DisputeID.String(),
		Status:    reconciliationv1.DisputeStatus_DISPUTE_STATUS_UNSPECIFIED,
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestListBalanceAssertions_Success(t *testing.T) {
	runID := uuid.New()
	lister := &mockAssertionLister{
		assertions: []*domain.BalanceAssertion{
			makeAssertionForRun(t, runID),
			makeAssertionForRun(t, runID),
		},
	}
	svc := service.NewAccountReconciliationService(
		service.WithAssertionListRepository(lister),
	)

	resp, err := svc.ListBalanceAssertions(context.Background(), &reconciliationv1.ListBalanceAssertionsRequest{
		RunId: runID.String(),
	})
	require.NoError(t, err)
	assert.Len(t, resp.Items, 2)
	assert.Empty(t, resp.NextPageToken)
	assert.Equal(t, int64(-1), resp.TotalCount)
}

func TestListBalanceAssertions_InvalidRunID(t *testing.T) {
	svc := service.NewAccountReconciliationService(
		service.WithAssertionListRepository(&mockAssertionLister{}),
	)

	_, err := svc.ListBalanceAssertions(context.Background(), &reconciliationv1.ListBalanceAssertionsRequest{
		RunId: "not-a-uuid",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestListBalanceAssertions_NotImplemented(t *testing.T) {
	svc := service.NewAccountReconciliationService()

	_, err := svc.ListBalanceAssertions(context.Background(), &reconciliationv1.ListBalanceAssertionsRequest{
		RunId: uuid.New().String(),
	})
	require.Error(t, err)
	assert.Equal(t, codes.Unimplemented, status.Code(err))
}

func TestListBalanceAssertions_Pagination(t *testing.T) {
	runID := uuid.New()
	assertions := make([]*domain.BalanceAssertion, 55)
	for i := range assertions {
		assertions[i] = makeAssertionForRun(t, runID)
	}
	lister := &mockAssertionLister{assertions: assertions}
	svc := service.NewAccountReconciliationService(
		service.WithAssertionListRepository(lister),
	)

	// First page
	resp, err := svc.ListBalanceAssertions(context.Background(), &reconciliationv1.ListBalanceAssertionsRequest{
		RunId:    runID.String(),
		PageSize: 50,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Items, 50)
	assert.NotEmpty(t, resp.NextPageToken)

	// Second page
	resp2, err := svc.ListBalanceAssertions(context.Background(), &reconciliationv1.ListBalanceAssertionsRequest{
		RunId:     runID.String(),
		PageSize:  50,
		PageToken: resp.NextPageToken,
	})
	require.NoError(t, err)
	assert.Len(t, resp2.Items, 5)
	assert.Empty(t, resp2.NextPageToken)
}

func TestListBalanceAssertions_InvalidPageToken(t *testing.T) {
	svc := service.NewAccountReconciliationService(
		service.WithAssertionListRepository(&mockAssertionLister{}),
	)

	_, err := svc.ListBalanceAssertions(context.Background(), &reconciliationv1.ListBalanceAssertionsRequest{
		RunId:     uuid.New().String(),
		PageToken: "not-base64!!",
	})
	require.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}
