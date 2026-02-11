package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// --- Mock payment method repository ---

type mockPMRepo struct {
	methods      map[uuid.UUID]*domain.PaymentMethod
	createErr    error
	updateErr    error
	findByIDErr  error
	listErr      error
	defaultErr   error
	defaultPM    *domain.PaymentMethod
	createCalled int
	updateCalled int
}

func newMockPMRepo() *mockPMRepo {
	return &mockPMRepo{
		methods: make(map[uuid.UUID]*domain.PaymentMethod),
	}
}

func (m *mockPMRepo) Create(_ context.Context, pm *domain.PaymentMethod) error {
	m.createCalled++
	if m.createErr != nil {
		return m.createErr
	}
	m.methods[pm.ID()] = pm
	return nil
}

func (m *mockPMRepo) Update(_ context.Context, pm *domain.PaymentMethod) error {
	m.updateCalled++
	if m.updateErr != nil {
		return m.updateErr
	}
	m.methods[pm.ID()] = pm
	return nil
}

func (m *mockPMRepo) FindByID(_ context.Context, id uuid.UUID) (*domain.PaymentMethod, error) {
	if m.findByIDErr != nil {
		return nil, m.findByIDErr
	}
	pm, ok := m.methods[id]
	if !ok {
		return nil, persistence.ErrPaymentMethodNotFound
	}
	return pm, nil
}

func (m *mockPMRepo) ListActiveByParty(_ context.Context, partyID uuid.UUID) ([]*domain.PaymentMethod, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	var result []*domain.PaymentMethod
	for _, pm := range m.methods {
		if pm.PartyID() == partyID && pm.Status() == domain.PaymentMethodStatusActive {
			result = append(result, pm)
		}
	}
	return result, nil
}

func (m *mockPMRepo) FindDefaultByParty(_ context.Context, _ uuid.UUID) (*domain.PaymentMethod, error) {
	if m.defaultErr != nil {
		return nil, m.defaultErr
	}
	return m.defaultPM, nil
}

// Verify interface compliance.
var _ PaymentMethodRepository = (*mockPMRepo)(nil)

func newTestServiceWithPM(mock *mockRepository, pmRepo *mockPMRepo) *Service {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, _ := NewService(mock, logger)
	svc.WithPaymentMethodRepository(pmRepo)
	return svc
}

func createTestParty(t *testing.T, repo *mockRepository) *domain.Party {
	t.Helper()
	party, err := domain.NewParty(domain.PartyTypePerson, "Test Person")
	require.NoError(t, err)
	repo.parties[party.ID()] = party
	return party
}

func createTestPaymentMethod(t *testing.T, partyID uuid.UUID, isDefault bool) *domain.PaymentMethod {
	t.Helper()
	pm, err := domain.NewPaymentMethod(
		partyID,
		domain.PaymentProviderStripe,
		"cus_testcustomer123",
		"pm_testmethod12345",
		domain.PaymentMethodTypeCard,
		isDefault,
		&domain.PaymentMethodMetadata{Last4: "4242", Brand: "visa"},
	)
	require.NoError(t, err)
	return pm
}

func TestAddPaymentMethod(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("adds payment method successfully", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		resp, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId:            party.ID().String(),
			Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
			ProviderCustomerId: "cus_testcustomer123",
			ProviderMethodId:   "pm_testmethod12345",
			MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
			IsDefault:          true,
			Metadata: map[string]string{
				"last4": "4242",
				"brand": "visa",
			},
		})

		require.NoError(t, err)
		require.NotNil(t, resp.PaymentMethod)
		assert.NotEmpty(t, resp.PaymentMethod.Id)
		assert.Equal(t, party.ID().String(), resp.PaymentMethod.PartyId)
		assert.Equal(t, pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE, resp.PaymentMethod.Provider)
		assert.Equal(t, pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD, resp.PaymentMethod.MethodType)
		assert.True(t, resp.PaymentMethod.IsDefault)
		assert.Equal(t, "4242", resp.PaymentMethod.Metadata["last4"])
		assert.Equal(t, "visa", resp.PaymentMethod.Metadata["brand"])
		assert.Equal(t, pb.PaymentMethodStatus_PAYMENT_METHOD_STATUS_ACTIVE, resp.PaymentMethod.Status)
		assert.Equal(t, 1, pmRepo.createCalled)
	})

	t.Run("adds payment method without default flag", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		resp, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId:            party.ID().String(),
			Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
			ProviderCustomerId: "cus_testcustomer123",
			ProviderMethodId:   "pm_testmethod12345",
			MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_BANK_ACCOUNT,
		})

		require.NoError(t, err)
		assert.False(t, resp.PaymentMethod.IsDefault)
		assert.Equal(t, pb.PaymentMethodType_PAYMENT_METHOD_TYPE_BANK_ACCOUNT, resp.PaymentMethod.MethodType)
	})

	t.Run("returns NOT_FOUND when party does not exist", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId:            uuid.New().String(),
			Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
			ProviderCustomerId: "cus_testcustomer123",
			ProviderMethodId:   "pm_testmethod12345",
			MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
		})

		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("returns ALREADY_EXISTS for duplicate provider method ID", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		pmRepo.createErr = persistence.ErrPaymentMethodExists
		party := createTestParty(t, partyRepo)
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId:            party.ID().String(),
			Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
			ProviderCustomerId: "cus_testcustomer123",
			ProviderMethodId:   "pm_testmethod12345",
			MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
		})

		assert.Equal(t, codes.AlreadyExists, status.Code(err))
	})

	t.Run("returns INVALID_ARGUMENT for invalid provider method ID", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId:            party.ID().String(),
			Provider:           pb.PaymentMethodProvider_PAYMENT_METHOD_PROVIDER_STRIPE,
			ProviderCustomerId: "cus_testcustomer123",
			ProviderMethodId:   "invalid_method_id",
			MethodType:         pb.PaymentMethodType_PAYMENT_METHOD_TYPE_CARD,
		})

		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("returns UNIMPLEMENTED when pmRepo not configured", func(t *testing.T) {
		partyRepo := newMockRepository()
		svc := newTestService(partyRepo) // no pmRepo

		_, err := svc.AddPaymentMethod(ctx, &pb.AddPaymentMethodRequest{
			PartyId: uuid.New().String(),
		})

		assert.Equal(t, codes.Unimplemented, status.Code(err))
	})
}

func TestRemovePaymentMethod(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("removes payment method successfully", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm := createTestPaymentMethod(t, party.ID(), false)
		pmRepo.methods[pm.ID()] = pm
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		resp, err := svc.RemovePaymentMethod(ctx, &pb.RemovePaymentMethodRequest{
			Id:      pm.ID().String(),
			Version: pm.Version(),
		})

		require.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Equal(t, 1, pmRepo.updateCalled)
	})

	t.Run("returns ABORTED for version mismatch", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm := createTestPaymentMethod(t, party.ID(), false)
		pmRepo.methods[pm.ID()] = pm
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.RemovePaymentMethod(ctx, &pb.RemovePaymentMethodRequest{
			Id:      pm.ID().String(),
			Version: 999,
		})

		assert.Equal(t, codes.Aborted, status.Code(err))
	})

	t.Run("returns NOT_FOUND for nonexistent payment method", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.RemovePaymentMethod(ctx, &pb.RemovePaymentMethodRequest{
			Id:      uuid.New().String(),
			Version: 1,
		})

		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("returns ABORTED on version conflict during update", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm := createTestPaymentMethod(t, party.ID(), false)
		pmRepo.methods[pm.ID()] = pm
		pmRepo.updateErr = persistence.ErrVersionConflict
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.RemovePaymentMethod(ctx, &pb.RemovePaymentMethodRequest{
			Id:      pm.ID().String(),
			Version: pm.Version(),
		})

		assert.Equal(t, codes.Aborted, status.Code(err))
	})
}

func TestSetDefaultPaymentMethod(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("sets payment method as default successfully", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm := createTestPaymentMethod(t, party.ID(), false)
		pmRepo.methods[pm.ID()] = pm
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		resp, err := svc.SetDefaultPaymentMethod(ctx, &pb.SetDefaultPaymentMethodRequest{
			Id: pm.ID().String(),
		})

		require.NoError(t, err)
		assert.True(t, resp.PaymentMethod.IsDefault)
		assert.Equal(t, 1, pmRepo.updateCalled)
	})

	t.Run("returns NOT_FOUND for nonexistent payment method", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.SetDefaultPaymentMethod(ctx, &pb.SetDefaultPaymentMethodRequest{
			Id: uuid.New().String(),
		})

		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("returns FAILED_PRECONDITION for removed payment method", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm := createTestPaymentMethod(t, party.ID(), false)
		require.NoError(t, pm.Remove())
		pmRepo.methods[pm.ID()] = pm
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.SetDefaultPaymentMethod(ctx, &pb.SetDefaultPaymentMethodRequest{
			Id: pm.ID().String(),
		})

		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	})
}

func TestListPaymentMethods(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns active payment methods for party", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm1 := createTestPaymentMethod(t, party.ID(), true)
		pm2 := createTestPaymentMethod(t, party.ID(), false)
		pmRepo.methods[pm1.ID()] = pm1
		pmRepo.methods[pm2.ID()] = pm2
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		resp, err := svc.ListPaymentMethods(ctx, &pb.ListPaymentMethodsRequest{
			PartyId: party.ID().String(),
		})

		require.NoError(t, err)
		assert.Len(t, resp.PaymentMethods, 2)
	})

	t.Run("returns empty list when no payment methods", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		resp, err := svc.ListPaymentMethods(ctx, &pb.ListPaymentMethodsRequest{
			PartyId: uuid.New().String(),
		})

		require.NoError(t, err)
		assert.Empty(t, resp.PaymentMethods)
	})

	t.Run("returns INTERNAL on repository error", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		pmRepo.listErr = errors.New("database timeout")
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.ListPaymentMethods(ctx, &pb.ListPaymentMethodsRequest{
			PartyId: uuid.New().String(),
		})

		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestGetDefaultPaymentMethod(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns default payment method", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		party := createTestParty(t, partyRepo)
		pm := createTestPaymentMethod(t, party.ID(), true)
		pmRepo.defaultPM = pm
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		resp, err := svc.GetDefaultPaymentMethod(ctx, &pb.GetDefaultPaymentMethodRequest{
			PartyId: party.ID().String(),
		})

		require.NoError(t, err)
		assert.True(t, resp.PaymentMethod.IsDefault)
		assert.Equal(t, pm.ID().String(), resp.PaymentMethod.Id)
	})

	t.Run("returns NOT_FOUND when no default exists", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.GetDefaultPaymentMethod(ctx, &pb.GetDefaultPaymentMethodRequest{
			PartyId: uuid.New().String(),
		})

		assert.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("returns INTERNAL on repository error", func(t *testing.T) {
		partyRepo := newMockRepository()
		pmRepo := newMockPMRepo()
		pmRepo.defaultErr = errors.New("database connection lost")
		svc := newTestServiceWithPM(partyRepo, pmRepo)

		_, err := svc.GetDefaultPaymentMethod(ctx, &pb.GetDefaultPaymentMethodRequest{
			PartyId: uuid.New().String(),
		})

		assert.Equal(t, codes.Internal, status.Code(err))
	})
}
