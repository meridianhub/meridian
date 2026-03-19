package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// demographicMockRepository extends mockRepository with configurable demographic behavior.
type demographicMockRepository struct {
	mockRepository
	saveDemoErr    error
	findDemoResult *persistence.PartyDemographicEntity
	findDemoErr    error
	saveBankErr    error
	findBankResult *persistence.PartyBankRelationEntity
	findBankErr    error
}

func (m *demographicMockRepository) SaveDemographic(_ context.Context, _ uuid.UUID, _, _ string) error {
	return m.saveDemoErr
}

func (m *demographicMockRepository) FindDemographic(_ context.Context, _ uuid.UUID) (*persistence.PartyDemographicEntity, error) {
	return m.findDemoResult, m.findDemoErr
}

func (m *demographicMockRepository) SaveBankRelation(_ context.Context, _ uuid.UUID, _, _, _ string) error {
	return m.saveBankErr
}

func (m *demographicMockRepository) FindBankRelation(_ context.Context, _ uuid.UUID) (*persistence.PartyBankRelationEntity, error) {
	return m.findBankResult, m.findBankErr
}

func newDemographicMock() *demographicMockRepository {
	return &demographicMockRepository{
		mockRepository: *newMockRepository(),
	}
}

func TestUpdateDemographics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("updates demographics successfully", func(t *testing.T) {
		mock := newDemographicMock()
		svc := newTestService(&mock.mockRepository)
		// Rewire the svc to use the demographic mock
		svc.repo = mock

		party, err := domain.NewParty(domain.PartyTypePerson, "Alice")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateDemographics(ctx, &pb.UpdateDemographicsRequest{
			PartyId:           party.ID().String(),
			SocioEconomicData: `{"income": "HIGH"}`,
			EmploymentHistory: `{"current": "Engineer"}`,
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, party.ID().String(), resp.PartyId)
		assert.Equal(t, `{"income": "HIGH"}`, resp.SocioEconomicData)
		assert.Equal(t, `{"current": "Engineer"}`, resp.EmploymentHistory)
		assert.NotNil(t, resp.UpdatedAt)
	})

	t.Run("returns InvalidArgument for invalid party ID", func(t *testing.T) {
		mock := newDemographicMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateDemographics(ctx, &pb.UpdateDemographicsRequest{
			PartyId: "bad-uuid",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns NotFound for non-existent party", func(t *testing.T) {
		mock := newDemographicMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateDemographics(ctx, &pb.UpdateDemographicsRequest{
			PartyId:           uuid.New().String(),
			SocioEconomicData: "data",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns Internal on find error", func(t *testing.T) {
		mock := newDemographicMock()
		mock.findByIDForUpdateErr = errDatabaseFailed
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateDemographics(ctx, &pb.UpdateDemographicsRequest{
			PartyId: uuid.New().String(),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("returns Internal on save error", func(t *testing.T) {
		mock := newDemographicMock()
		mock.saveDemoErr = errors.New("db write error")
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party, err := domain.NewParty(domain.PartyTypePerson, "Bob")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateDemographics(ctx, &pb.UpdateDemographicsRequest{
			PartyId:           party.ID().String(),
			SocioEconomicData: "data",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestRetrieveDemographics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("retrieves demographics with data", func(t *testing.T) {
		mock := newDemographicMock()
		socioData := `"HIGH_INCOME"`
		employData := `"Software Engineer"`
		mock.findDemoResult = &persistence.PartyDemographicEntity{
			SocioEconomicData: &socioData,
			EmploymentHistory: &employData,
			UpdatedAt:         time.Now(),
		}
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveDemographics(ctx, &pb.RetrieveDemographicsRequest{
			PartyId: uuid.New().String(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "HIGH_INCOME", resp.SocioEconomicData)
		assert.Equal(t, "Software Engineer", resp.EmploymentHistory)
		assert.NotNil(t, resp.UpdatedAt)
	})

	t.Run("retrieves empty demographics when none exist", func(t *testing.T) {
		mock := newDemographicMock()
		mock.findDemoResult = nil
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveDemographics(ctx, &pb.RetrieveDemographicsRequest{
			PartyId: uuid.New().String(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Empty(t, resp.SocioEconomicData)
		assert.Empty(t, resp.EmploymentHistory)
	})

	t.Run("returns InvalidArgument for invalid party ID", func(t *testing.T) {
		mock := newDemographicMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveDemographics(ctx, &pb.RetrieveDemographicsRequest{
			PartyId: "invalid",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns Internal on repository error", func(t *testing.T) {
		mock := newDemographicMock()
		mock.findDemoErr = errDatabaseFailed
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveDemographics(ctx, &pb.RetrieveDemographicsRequest{
			PartyId: uuid.New().String(),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestUpdateBankRelations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("updates bank relations successfully", func(t *testing.T) {
		mock := newDemographicMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party, err := domain.NewParty(domain.PartyTypePerson, "Alice")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateBankRelations(ctx, &pb.UpdateBankRelationsRequest{
			PartyId:               party.ID().String(),
			AccountOfficerId:      "officer-1",
			RelationshipManagerId: "rm-1",
			AssignedBranch:        "LONDON-HQ",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "officer-1", resp.AccountOfficerId)
		assert.Equal(t, "rm-1", resp.RelationshipManagerId)
		assert.Equal(t, "LONDON-HQ", resp.AssignedBranch)
		assert.NotNil(t, resp.UpdatedAt)
	})

	t.Run("returns InvalidArgument for invalid party ID", func(t *testing.T) {
		mock := newDemographicMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateBankRelations(ctx, &pb.UpdateBankRelationsRequest{
			PartyId: "bad",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns NotFound for non-existent party", func(t *testing.T) {
		mock := newDemographicMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateBankRelations(ctx, &pb.UpdateBankRelationsRequest{
			PartyId: uuid.New().String(),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns Internal on find error", func(t *testing.T) {
		mock := newDemographicMock()
		mock.findByIDForUpdateErr = errDatabaseFailed
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateBankRelations(ctx, &pb.UpdateBankRelationsRequest{
			PartyId: uuid.New().String(),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("returns Internal on save error", func(t *testing.T) {
		mock := newDemographicMock()
		mock.saveBankErr = errors.New("db error")
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party, err := domain.NewParty(domain.PartyTypePerson, "Bob")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateBankRelations(ctx, &pb.UpdateBankRelationsRequest{
			PartyId:          party.ID().String(),
			AccountOfficerId: "officer",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestRetrieveBankRelations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("retrieves bank relations with data", func(t *testing.T) {
		mock := newDemographicMock()
		officer := "officer-1"
		rm := "rm-1"
		branch := "LONDON"
		mock.findBankResult = &persistence.PartyBankRelationEntity{
			AccountOfficerID:      &officer,
			RelationshipManagerID: &rm,
			AssignedBranch:        &branch,
			UpdatedAt:             time.Now(),
		}
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveBankRelations(ctx, &pb.RetrieveBankRelationsRequest{
			PartyId: uuid.New().String(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "officer-1", resp.AccountOfficerId)
		assert.Equal(t, "rm-1", resp.RelationshipManagerId)
		assert.Equal(t, "LONDON", resp.AssignedBranch)
		assert.NotNil(t, resp.UpdatedAt)
	})

	t.Run("retrieves empty when no bank relation exists", func(t *testing.T) {
		mock := newDemographicMock()
		mock.findBankResult = nil
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveBankRelations(ctx, &pb.RetrieveBankRelationsRequest{
			PartyId: uuid.New().String(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Empty(t, resp.AccountOfficerId)
	})

	t.Run("retrieves bank relation with partial data", func(t *testing.T) {
		mock := newDemographicMock()
		officer := "officer-only"
		mock.findBankResult = &persistence.PartyBankRelationEntity{
			AccountOfficerID: &officer,
			UpdatedAt:        time.Now(),
		}
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveBankRelations(ctx, &pb.RetrieveBankRelationsRequest{
			PartyId: uuid.New().String(),
		})
		require.NoError(t, err)
		assert.Equal(t, "officer-only", resp.AccountOfficerId)
		assert.Empty(t, resp.RelationshipManagerId)
		assert.Empty(t, resp.AssignedBranch)
	})

	t.Run("returns InvalidArgument for invalid party ID", func(t *testing.T) {
		mock := newDemographicMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveBankRelations(ctx, &pb.RetrieveBankRelationsRequest{
			PartyId: "nope",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns Internal on repository error", func(t *testing.T) {
		mock := newDemographicMock()
		mock.findBankErr = errDatabaseFailed
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveBankRelations(ctx, &pb.RetrieveBankRelationsRequest{
			PartyId: uuid.New().String(),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestExchangeDemographicsStub(t *testing.T) {
	ctx := context.Background()

	t.Run("stub returns VERIFIED in test environment", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Test")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		t.Setenv("ENVIRONMENT", "test")

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: party.ID().String(),
		})
		require.NoError(t, err)
		assert.Equal(t, "VERIFIED", resp.VerificationStatus)
	})

	t.Run("stub returns Unimplemented in unknown environment", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Test")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		t.Setenv("ENVIRONMENT", "staging")

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: party.ID().String(),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Unimplemented, st.Code())
	})

	t.Run("returns Internal on find error", func(t *testing.T) {
		mock := newMockRepository()
		mock.findByIDErr = errDatabaseFailed
		svc := newTestService(mock)

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: uuid.New().String(),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}
