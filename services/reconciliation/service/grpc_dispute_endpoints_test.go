package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	reconciliationv1 "github.com/meridianhub/meridian/api/proto/meridian/reconciliation/v1"
	"github.com/meridianhub/meridian/services/reconciliation/domain"
	"github.com/meridianhub/meridian/services/reconciliation/service"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockDisputeRepo implements domain.DisputeRepository for testing.
type mockDisputeRepo struct {
	disputes map[uuid.UUID]*domain.Dispute
}

func newMockDisputeRepo() *mockDisputeRepo {
	return &mockDisputeRepo{disputes: make(map[uuid.UUID]*domain.Dispute)}
}

func (m *mockDisputeRepo) Create(_ context.Context, d *domain.Dispute) error {
	m.disputes[d.DisputeID] = d
	return nil
}

func (m *mockDisputeRepo) FindByID(_ context.Context, id uuid.UUID) (*domain.Dispute, error) {
	for _, d := range m.disputes {
		if d.DisputeID == id {
			return d, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (m *mockDisputeRepo) FindByVarianceID(_ context.Context, varianceID uuid.UUID) ([]*domain.Dispute, error) {
	result := make([]*domain.Dispute, 0, len(m.disputes))
	for _, d := range m.disputes {
		if d.VarianceID == varianceID {
			result = append(result, d)
		}
	}
	return result, nil
}

func (m *mockDisputeRepo) Update(_ context.Context, d *domain.Dispute) error {
	if _, ok := m.disputes[d.DisputeID]; !ok {
		return domain.ErrNotFound
	}
	m.disputes[d.DisputeID] = d
	return nil
}

func (m *mockDisputeRepo) List(_ context.Context, filter domain.DisputeFilter) ([]*domain.Dispute, error) {
	result := make([]*domain.Dispute, 0, len(m.disputes))
	for _, d := range m.disputes {
		if filter.Status != nil && d.Status != *filter.Status {
			continue
		}
		if filter.AccountID != nil && d.AccountID != *filter.AccountID {
			continue
		}
		result = append(result, d)
	}
	return result, nil
}

// mockVarianceRepo implements service.VarianceFinder for testing.
type mockVarianceRepo struct {
	variances map[uuid.UUID]*domain.Variance
}

func newMockVarianceRepo() *mockVarianceRepo {
	return &mockVarianceRepo{variances: make(map[uuid.UUID]*domain.Variance)}
}

func (m *mockVarianceRepo) FindByID(_ context.Context, id uuid.UUID) (*domain.Variance, error) {
	v, ok := m.variances[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return v, nil
}

func (m *mockVarianceRepo) UpdateStatus(_ context.Context, id uuid.UUID, s domain.VarianceStatus) error {
	v, ok := m.variances[id]
	if !ok {
		return domain.ErrNotFound
	}
	v.Status = s
	return nil
}

// mockEventPublisher implements service.EventPublisher for testing.
type mockEventPublisher struct {
	events []interface{}
}

func (m *mockEventPublisher) Publish(_ context.Context, _ string, event interface{}) error {
	m.events = append(m.events, event)
	return nil
}

// mockSagaRuntime implements service.SagaRuntime for testing.
type mockSagaRuntime struct {
	invocations []string
}

func (m *mockSagaRuntime) InvokeSaga(_ context.Context, name string, _ map[string]interface{}) error {
	m.invocations = append(m.invocations, name)
	return nil
}

// ctxWithClaims creates a context with the given auth claims.
func ctxWithClaims(roles ...string) context.Context {
	claims := &auth.Claims{
		UserID: "test-user",
		Roles:  roles,
	}
	return context.WithValue(context.Background(), auth.ClaimsContextKey, claims)
}

func TestInitiateDispute(t *testing.T) {
	varianceID := uuid.New()
	runID := uuid.New()

	varianceRepo := newMockVarianceRepo()
	varianceRepo.variances[varianceID] = &domain.Variance{
		VarianceID: varianceID,
		RunID:      runID,
		AccountID:  "ACC-001",
		Status:     domain.VarianceStatusOpen,
	}

	disputeRepo := newMockDisputeRepo()
	eventPub := &mockEventPublisher{}

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(disputeRepo),
		service.WithVarianceRepository(varianceRepo),
		service.WithEventPublisher(eventPub),
	)

	t.Run("successful dispute creation", func(t *testing.T) {
		resp, err := svc.InitiateDispute(context.Background(), &reconciliationv1.InitiateDisputeRequest{
			VarianceId: varianceID.String(),
			RunId:      runID.String(),
			AccountId:  "ACC-001",
			Reason:     "Amount seems incorrect",
			RaisedBy:   "user-1",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Dispute)

		assert.Equal(t, "ACC-001", resp.Dispute.AccountId)
		assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_OPEN, resp.Dispute.Status)
		assert.Equal(t, "Amount seems incorrect", resp.Dispute.Reason)
		assert.Equal(t, "user-1", resp.Dispute.RaisedBy)
		assert.Equal(t, varianceID.String(), resp.Dispute.VarianceId)

		// Verify variance was marked as disputed
		assert.Equal(t, domain.VarianceStatusDisputed, varianceRepo.variances[varianceID].Status)

		// Verify event was published
		assert.Len(t, eventPub.events, 1)
	})

	t.Run("invalid variance ID", func(t *testing.T) {
		_, err := svc.InitiateDispute(context.Background(), &reconciliationv1.InitiateDisputeRequest{
			VarianceId: "not-a-uuid",
			RunId:      runID.String(),
			AccountId:  "ACC-001",
			Reason:     "Reason",
			RaisedBy:   "user-1",
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("variance not found", func(t *testing.T) {
		_, err := svc.InitiateDispute(context.Background(), &reconciliationv1.InitiateDisputeRequest{
			VarianceId: uuid.New().String(),
			RunId:      runID.String(),
			AccountId:  "ACC-001",
			Reason:     "Reason",
			RaisedBy:   "user-1",
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("empty reason", func(t *testing.T) {
		_, err := svc.InitiateDispute(context.Background(), &reconciliationv1.InitiateDisputeRequest{
			VarianceId: varianceID.String(),
			RunId:      runID.String(),
			AccountId:  "ACC-001",
			Reason:     "",
			RaisedBy:   "user-1",
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func TestControlDispute_Escalate(t *testing.T) {
	disputeRepo := newMockDisputeRepo()

	// Create a dispute in UNDER_REVIEW state (escalate requires UNDER_REVIEW)
	d := createTestDispute(t)
	require.NoError(t, d.Review())
	disputeRepo.disputes[d.DisputeID] = d

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(disputeRepo),
	)

	resp, err := svc.ControlDispute(context.Background(), &reconciliationv1.ControlDisputeRequest{
		DisputeId: d.DisputeID.String(),
		Action:    reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_ESCALATE,
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_ESCALATED, resp.Dispute.Status)
}

func TestControlDispute_Resolve(t *testing.T) {
	disputeRepo := newMockDisputeRepo()
	sagaRT := &mockSagaRuntime{}
	eventPub := &mockEventPublisher{}

	d := createTestDispute(t)
	disputeRepo.disputes[d.DisputeID] = d

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(disputeRepo),
		service.WithSagaRuntime(sagaRT),
		service.WithEventPublisher(eventPub),
	)

	t.Run("requires admin or operator role", func(t *testing.T) {
		// No claims in context
		_, err := svc.ControlDispute(context.Background(), &reconciliationv1.ControlDisputeRequest{
			DisputeId:  d.DisputeID.String(),
			Action:     reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_RESOLVE,
			Resolution: "Adjustment posted",
			ResolvedBy: "admin-user",
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("auditor role denied", func(t *testing.T) {
		ctx := ctxWithClaims("auditor")
		_, err := svc.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{
			DisputeId:  d.DisputeID.String(),
			Action:     reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_RESOLVE,
			Resolution: "Adjustment posted",
			ResolvedBy: "auditor-user",
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.PermissionDenied, st.Code())
	})

	t.Run("admin can resolve", func(t *testing.T) {
		ctx := ctxWithClaims("admin")
		resp, err := svc.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{
			DisputeId:  d.DisputeID.String(),
			Action:     reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_RESOLVE,
			Resolution: "Adjustment posted",
			ResolvedBy: "admin-user",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_RESOLVED, resp.Dispute.Status)
		assert.Equal(t, "Adjustment posted", resp.Dispute.Resolution)
		assert.Equal(t, "admin-user", resp.Dispute.ResolvedBy)

		// Verify saga was invoked
		assert.Contains(t, sagaRT.invocations, "reconciliation_adjustment")

		// Verify event was published
		assert.Len(t, eventPub.events, 1)
	})
}

func TestControlDispute_Reject(t *testing.T) {
	disputeRepo := newMockDisputeRepo()
	eventPub := &mockEventPublisher{}

	d := createTestDispute(t)
	disputeRepo.disputes[d.DisputeID] = d

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(disputeRepo),
		service.WithEventPublisher(eventPub),
	)

	ctx := ctxWithClaims("operator")
	resp, err := svc.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{
		DisputeId:  d.DisputeID.String(),
		Action:     reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_REJECT,
		Resolution: "No evidence of error",
		ResolvedBy: "operator-user",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, reconciliationv1.DisputeStatus_DISPUTE_STATUS_REJECTED, resp.Dispute.Status)
	assert.Equal(t, "No evidence of error", resp.Dispute.Resolution)

	// Verify event was published
	assert.Len(t, eventPub.events, 1)
}

func TestControlDispute_InvalidTransition(t *testing.T) {
	disputeRepo := newMockDisputeRepo()

	// Create resolved dispute - no further transitions allowed
	d := createTestDispute(t)
	require.NoError(t, d.Resolve("done", "admin"))
	disputeRepo.disputes[d.DisputeID] = d

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(disputeRepo),
	)

	ctx := ctxWithClaims("admin")
	_, err := svc.ControlDispute(ctx, &reconciliationv1.ControlDisputeRequest{
		DisputeId:  d.DisputeID.String(),
		Action:     reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_REJECT,
		Resolution: "Trying to reject resolved",
		ResolvedBy: "admin-user",
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.FailedPrecondition, st.Code())
}

func TestControlDispute_NotFound(t *testing.T) {
	disputeRepo := newMockDisputeRepo()

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(disputeRepo),
	)

	_, err := svc.ControlDispute(context.Background(), &reconciliationv1.ControlDisputeRequest{
		DisputeId: uuid.New().String(),
		Action:    reconciliationv1.DisputeControlAction_DISPUTE_CONTROL_ACTION_ESCALATE,
	})
	require.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestRetrieveDispute(t *testing.T) {
	disputeRepo := newMockDisputeRepo()
	d := createTestDispute(t)
	disputeRepo.disputes[d.DisputeID] = d

	svc := service.NewAccountReconciliationService(
		service.WithDisputeRepository(disputeRepo),
	)

	t.Run("found", func(t *testing.T) {
		resp, err := svc.RetrieveDispute(context.Background(), &reconciliationv1.RetrieveDisputeRequest{
			DisputeId: d.DisputeID.String(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, d.DisputeID.String(), resp.Dispute.DisputeId)
		assert.Equal(t, d.AccountID, resp.Dispute.AccountId)
	})

	t.Run("not found", func(t *testing.T) {
		_, err := svc.RetrieveDispute(context.Background(), &reconciliationv1.RetrieveDisputeRequest{
			DisputeId: uuid.New().String(),
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("invalid UUID", func(t *testing.T) {
		_, err := svc.RetrieveDispute(context.Background(), &reconciliationv1.RetrieveDisputeRequest{
			DisputeId: "bad-uuid",
		})
		require.Error(t, err)
		st, _ := status.FromError(err)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func createTestDispute(t *testing.T) *domain.Dispute {
	t.Helper()
	d, err := domain.NewDispute(
		uuid.New(), uuid.New(), "ACC-001",
		"Amount discrepancy", "user-1",
	)
	require.NoError(t, err)
	return d
}
