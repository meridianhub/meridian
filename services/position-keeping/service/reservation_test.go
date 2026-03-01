package service_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/domain"
	"github.com/meridianhub/meridian/services/position-keeping/service"
)

// MockReservationRepository is a mock implementation of domain.ReservationRepository.
type MockReservationRepository struct {
	mock.Mock
}

func (m *MockReservationRepository) Create(ctx context.Context, reservation *domain.Reservation) error {
	args := m.Called(ctx, reservation)
	return args.Error(0)
}

func (m *MockReservationRepository) FindByLienID(ctx context.Context, lienID uuid.UUID) (*domain.Reservation, error) {
	args := m.Called(ctx, lienID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Reservation), args.Error(1)
}

func (m *MockReservationRepository) UpdateStatus(ctx context.Context, lienID uuid.UUID, newStatus domain.ReservationStatus) error {
	args := m.Called(ctx, lienID, newStatus)
	return args.Error(0)
}

func (m *MockReservationRepository) SumActiveReservations(ctx context.Context, accountID, instrumentCode, bucketID string) (decimal.Decimal, error) {
	args := m.Called(ctx, accountID, instrumentCode, bucketID)
	return args.Get(0).(decimal.Decimal), args.Error(1)
}

// MockPositionRepository is a mock implementation of domain.PositionRepository.
type MockPositionRepository struct {
	mock.Mock
}

func (m *MockPositionRepository) Insert(ctx context.Context, position *domain.Position) error {
	args := m.Called(ctx, position)
	return args.Error(0)
}

func (m *MockPositionRepository) InsertBatch(ctx context.Context, positions []*domain.Position) error {
	args := m.Called(ctx, positions)
	return args.Error(0)
}

func (m *MockPositionRepository) FindByID(ctx context.Context, id uuid.UUID) (*domain.Position, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Position), args.Error(1)
}

func (m *MockPositionRepository) GetAggregatedPosition(ctx context.Context, accountID, instrumentCode, bucketKey string) (*domain.AggregatedPosition, error) {
	args := m.Called(ctx, accountID, instrumentCode, bucketKey)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.AggregatedPosition), args.Error(1)
}

func (m *MockPositionRepository) ListByAccount(ctx context.Context, accountID string, limit, offset int) ([]*domain.Position, error) {
	args := m.Called(ctx, accountID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Position), args.Error(1)
}

func (m *MockPositionRepository) ListAggregatedByAccount(ctx context.Context, accountID string) ([]*domain.AggregatedPosition, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.AggregatedPosition), args.Error(1)
}

func (m *MockPositionRepository) GetPositionCount(ctx context.Context, accountID string) (int64, error) {
	args := m.Called(ctx, accountID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockPositionRepository) GetAggregatedPositions(ctx context.Context, accountID, instrumentCode string) ([]*domain.AggregatedPosition, error) {
	args := m.Called(ctx, accountID, instrumentCode)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.AggregatedPosition), args.Error(1)
}

func (m *MockPositionRepository) GetBucketDetails(ctx context.Context, accountID, instrumentCode, bucketKey string, limit, offset int) ([]*domain.Position, error) {
	args := m.Called(ctx, accountID, instrumentCode, bucketKey, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Position), args.Error(1)
}

func (m *MockPositionRepository) SoftDelete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockPositionRepository) SoftDeleteBatch(ctx context.Context, ids []uuid.UUID) error {
	args := m.Called(ctx, ids)
	return args.Error(0)
}

func (m *MockPositionRepository) UpdateAttributes(ctx context.Context, id uuid.UUID, attributes map[string]string) error {
	args := m.Called(ctx, id, attributes)
	return args.Error(0)
}

func newServiceWithReservation(t *testing.T, reservationRepo domain.ReservationRepository, positionRepo domain.PositionRepository) *service.PositionKeepingService {
	t.Helper()
	repo := new(MockRepository)
	measurementRepo := new(MockMeasurementRepository)
	publisher := domain.NewInMemoryEventPublisher()
	idempotencySvc := new(MockIdempotencyService)

	var opts []service.Option
	if reservationRepo != nil {
		opts = append(opts, service.WithReservationRepository(reservationRepo))
	}
	if positionRepo != nil {
		opts = append(opts, service.WithPositionRepository(positionRepo))
	}

	svc, err := service.NewPositionKeepingService(repo, measurementRepo, publisher, idempotencySvc, newTestOutboxPublisher(t), opts...)
	require.NoError(t, err)
	return svc
}

func TestRecordReservation(t *testing.T) {
	ctx := context.Background()

	t.Run("creates new reservation", func(t *testing.T) {
		reservationRepo := new(MockReservationRepository)
		reservationRepo.On("Create", ctx, mock.AnythingOfType("*domain.Reservation")).Return(nil)

		svc := newServiceWithReservation(t, reservationRepo, nil)
		lienID := uuid.New()

		resp, err := svc.RecordReservation(ctx, &positionkeepingv1.RecordReservationRequest{
			AccountId:      "acc-1",
			LienId:         lienID.String(),
			InstrumentCode: "GBP",
			ReservedAmount: "100.00",
			BucketId:       "bucket-1",
		})

		require.NoError(t, err)
		assert.Equal(t, lienID.String(), resp.ReservationId)
		assert.False(t, resp.AlreadyExists)
		reservationRepo.AssertExpectations(t)
	})

	t.Run("returns already_exists for duplicate lien_id", func(t *testing.T) {
		reservationRepo := new(MockReservationRepository)
		reservationRepo.On("Create", ctx, mock.AnythingOfType("*domain.Reservation")).Return(domain.ErrConflict)

		svc := newServiceWithReservation(t, reservationRepo, nil)
		lienID := uuid.New()

		resp, err := svc.RecordReservation(ctx, &positionkeepingv1.RecordReservationRequest{
			AccountId:      "acc-1",
			LienId:         lienID.String(),
			InstrumentCode: "GBP",
			ReservedAmount: "100.00",
		})

		require.NoError(t, err)
		assert.Equal(t, lienID.String(), resp.ReservationId)
		assert.True(t, resp.AlreadyExists)
	})

	t.Run("rejects empty account_id", func(t *testing.T) {
		svc := newServiceWithReservation(t, new(MockReservationRepository), nil)

		_, err := svc.RecordReservation(ctx, &positionkeepingv1.RecordReservationRequest{
			LienId:         uuid.New().String(),
			InstrumentCode: "GBP",
			ReservedAmount: "100.00",
		})

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("rejects invalid lien_id", func(t *testing.T) {
		svc := newServiceWithReservation(t, new(MockReservationRepository), nil)

		_, err := svc.RecordReservation(ctx, &positionkeepingv1.RecordReservationRequest{
			AccountId:      "acc-1",
			LienId:         "not-a-uuid",
			InstrumentCode: "GBP",
			ReservedAmount: "100.00",
		})

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("rejects invalid amount", func(t *testing.T) {
		svc := newServiceWithReservation(t, new(MockReservationRepository), nil)

		_, err := svc.RecordReservation(ctx, &positionkeepingv1.RecordReservationRequest{
			AccountId:      "acc-1",
			LienId:         uuid.New().String(),
			InstrumentCode: "GBP",
			ReservedAmount: "not-a-number",
		})

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns FailedPrecondition when repo not configured", func(t *testing.T) {
		svc := newServiceWithReservation(t, nil, nil)

		_, err := svc.RecordReservation(ctx, &positionkeepingv1.RecordReservationRequest{
			AccountId:      "acc-1",
			LienId:         uuid.New().String(),
			InstrumentCode: "GBP",
			ReservedAmount: "100.00",
		})

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})
}

func TestReleaseReservation(t *testing.T) {
	ctx := context.Background()

	t.Run("releases to EXECUTED", func(t *testing.T) {
		reservationRepo := new(MockReservationRepository)
		lienID := uuid.New()
		reservationRepo.On("UpdateStatus", ctx, lienID, domain.ReservationStatusExecuted).Return(nil)

		svc := newServiceWithReservation(t, reservationRepo, nil)

		resp, err := svc.ReleaseReservation(ctx, &positionkeepingv1.ReleaseReservationRequest{
			LienId: lienID.String(),
			Reason: positionkeepingv1.ReservationStatus_RESERVATION_STATUS_EXECUTED,
		})

		require.NoError(t, err)
		assert.True(t, resp.Released)
		reservationRepo.AssertExpectations(t)
	})

	t.Run("releases to TERMINATED", func(t *testing.T) {
		reservationRepo := new(MockReservationRepository)
		lienID := uuid.New()
		reservationRepo.On("UpdateStatus", ctx, lienID, domain.ReservationStatusTerminated).Return(nil)

		svc := newServiceWithReservation(t, reservationRepo, nil)

		resp, err := svc.ReleaseReservation(ctx, &positionkeepingv1.ReleaseReservationRequest{
			LienId: lienID.String(),
			Reason: positionkeepingv1.ReservationStatus_RESERVATION_STATUS_TERMINATED,
		})

		require.NoError(t, err)
		assert.True(t, resp.Released)
	})

	t.Run("returns NotFound for missing reservation", func(t *testing.T) {
		reservationRepo := new(MockReservationRepository)
		lienID := uuid.New()
		reservationRepo.On("UpdateStatus", ctx, lienID, domain.ReservationStatusExecuted).Return(domain.ErrReservationNotFound)

		svc := newServiceWithReservation(t, reservationRepo, nil)

		_, err := svc.ReleaseReservation(ctx, &positionkeepingv1.ReleaseReservationRequest{
			LienId: lienID.String(),
			Reason: positionkeepingv1.ReservationStatus_RESERVATION_STATUS_EXECUTED,
		})

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns FailedPrecondition for already terminal reservation", func(t *testing.T) {
		reservationRepo := new(MockReservationRepository)
		lienID := uuid.New()
		reservationRepo.On("UpdateStatus", ctx, lienID, domain.ReservationStatusExecuted).Return(domain.ErrReservationAlreadyFinal)

		svc := newServiceWithReservation(t, reservationRepo, nil)

		_, err := svc.ReleaseReservation(ctx, &positionkeepingv1.ReleaseReservationRequest{
			LienId: lienID.String(),
			Reason: positionkeepingv1.ReservationStatus_RESERVATION_STATUS_EXECUTED,
		})

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("rejects invalid reason", func(t *testing.T) {
		svc := newServiceWithReservation(t, new(MockReservationRepository), nil)

		_, err := svc.ReleaseReservation(ctx, &positionkeepingv1.ReleaseReservationRequest{
			LienId: uuid.New().String(),
			Reason: positionkeepingv1.ReservationStatus_RESERVATION_STATUS_ACTIVE,
		})

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

func TestGetProjectedBalance(t *testing.T) {
	ctx := context.Background()

	t.Run("returns projected balance with bucket filter", func(t *testing.T) {
		reservationRepo := new(MockReservationRepository)
		positionRepo := new(MockPositionRepository)

		positionRepo.On("GetAggregatedPosition", ctx, "acc-1", "GBP", "bucket-1").
			Return(&domain.AggregatedPosition{TotalAmount: decimal.NewFromInt(1000)}, nil)
		reservationRepo.On("SumActiveReservations", ctx, "acc-1", "GBP", "bucket-1").
			Return(decimal.NewFromInt(200), nil)

		svc := newServiceWithReservation(t, reservationRepo, positionRepo)

		resp, err := svc.GetProjectedBalance(ctx, &positionkeepingv1.GetProjectedBalanceRequest{
			AccountId:      "acc-1",
			InstrumentCode: "GBP",
			BucketId:       "bucket-1",
		})

		require.NoError(t, err)
		assert.Equal(t, "1000", resp.CurrentBalance)
		assert.Equal(t, "200", resp.ActiveReservationsTotal)
		assert.Equal(t, "800", resp.ProjectedBalance)
		assert.Equal(t, "bucket-1", resp.BucketId)
		assert.Equal(t, "GBP", resp.InstrumentCode)
	})

	t.Run("returns projected balance without bucket filter", func(t *testing.T) {
		reservationRepo := new(MockReservationRepository)
		positionRepo := new(MockPositionRepository)

		positionRepo.On("GetAggregatedPositions", ctx, "acc-1", "GBP").
			Return([]*domain.AggregatedPosition{
				{TotalAmount: decimal.NewFromInt(500)},
				{TotalAmount: decimal.NewFromInt(300)},
			}, nil)
		reservationRepo.On("SumActiveReservations", ctx, "acc-1", "GBP", "").
			Return(decimal.NewFromInt(100), nil)

		svc := newServiceWithReservation(t, reservationRepo, positionRepo)

		resp, err := svc.GetProjectedBalance(ctx, &positionkeepingv1.GetProjectedBalanceRequest{
			AccountId:      "acc-1",
			InstrumentCode: "GBP",
		})

		require.NoError(t, err)
		assert.Equal(t, "800", resp.CurrentBalance)
		assert.Equal(t, "100", resp.ActiveReservationsTotal)
		assert.Equal(t, "700", resp.ProjectedBalance)
	})

	t.Run("returns zero balance when no positions exist", func(t *testing.T) {
		reservationRepo := new(MockReservationRepository)
		positionRepo := new(MockPositionRepository)

		positionRepo.On("GetAggregatedPosition", ctx, "acc-1", "GBP", "bucket-1").
			Return(nil, nil)
		reservationRepo.On("SumActiveReservations", ctx, "acc-1", "GBP", "bucket-1").
			Return(decimal.Zero, nil)

		svc := newServiceWithReservation(t, reservationRepo, positionRepo)

		resp, err := svc.GetProjectedBalance(ctx, &positionkeepingv1.GetProjectedBalanceRequest{
			AccountId:      "acc-1",
			InstrumentCode: "GBP",
			BucketId:       "bucket-1",
		})

		require.NoError(t, err)
		assert.Equal(t, "0", resp.CurrentBalance)
		assert.Equal(t, "0", resp.ActiveReservationsTotal)
		assert.Equal(t, "0", resp.ProjectedBalance)
	})

	t.Run("rejects empty account_id", func(t *testing.T) {
		svc := newServiceWithReservation(t, new(MockReservationRepository), new(MockPositionRepository))

		_, err := svc.GetProjectedBalance(ctx, &positionkeepingv1.GetProjectedBalanceRequest{
			InstrumentCode: "GBP",
		})

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("rejects empty instrument_code", func(t *testing.T) {
		svc := newServiceWithReservation(t, new(MockReservationRepository), new(MockPositionRepository))

		_, err := svc.GetProjectedBalance(ctx, &positionkeepingv1.GetProjectedBalanceRequest{
			AccountId: "acc-1",
		})

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns FailedPrecondition when repos not configured", func(t *testing.T) {
		svc := newServiceWithReservation(t, nil, nil)

		_, err := svc.GetProjectedBalance(ctx, &positionkeepingv1.GetProjectedBalanceRequest{
			AccountId:      "acc-1",
			InstrumentCode: "GBP",
		})

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})
}

func TestRecordReservation_Idempotency(t *testing.T) {
	ctx := context.Background()

	t.Run("three retries with same lien_id all succeed", func(t *testing.T) {
		reservationRepo := new(MockReservationRepository)

		// First call: create succeeds
		reservationRepo.On("Create", ctx, mock.AnythingOfType("*domain.Reservation")).Return(nil).Once()
		// Second and third calls: conflict (already exists)
		reservationRepo.On("Create", ctx, mock.AnythingOfType("*domain.Reservation")).Return(domain.ErrConflict).Twice()

		svc := newServiceWithReservation(t, reservationRepo, nil)
		lienID := uuid.New()

		req := &positionkeepingv1.RecordReservationRequest{
			AccountId:      "acc-1",
			LienId:         lienID.String(),
			InstrumentCode: "GBP",
			ReservedAmount: "100.00",
		}

		// First call
		resp1, err := svc.RecordReservation(ctx, req)
		require.NoError(t, err)
		assert.False(t, resp1.AlreadyExists)
		assert.Equal(t, lienID.String(), resp1.ReservationId)

		// Second call
		resp2, err := svc.RecordReservation(ctx, req)
		require.NoError(t, err)
		assert.True(t, resp2.AlreadyExists)

		// Third call
		resp3, err := svc.RecordReservation(ctx, req)
		require.NoError(t, err)
		assert.True(t, resp3.AlreadyExists)
	})
}
