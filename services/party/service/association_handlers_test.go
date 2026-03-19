package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// associationMockRepository extends mockRepository with configurable association behavior.
type associationMockRepository struct {
	mockRepository
	checkCircularResult bool
	checkCircularErr    error
	saveAssocErr        error
	findAssocResult     []persistence.PartyAssociationEntity
	findAssocErr        error
	updateAssocResult   *persistence.PartyAssociationEntity
	updateAssocErr      error
	listParticipantsResult []persistence.PartyAssociationEntity
	listParticipantsErr    error
	getStructuringResult   map[string]interface{}
	getStructuringErr      error
}

func (m *associationMockRepository) CheckCircularAssociation(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return m.checkCircularResult, m.checkCircularErr
}

func (m *associationMockRepository) SaveAssociationWithInput(_ context.Context, _, _ uuid.UUID, _ string, _ *persistence.AssociationInput) (uuid.UUID, error) {
	if m.saveAssocErr != nil {
		return uuid.Nil, m.saveAssocErr
	}
	return uuid.New(), nil
}

func (m *associationMockRepository) FindAssociations(_ context.Context, _ uuid.UUID) ([]persistence.PartyAssociationEntity, error) {
	return m.findAssocResult, m.findAssocErr
}

func (m *associationMockRepository) UpdateAssociation(_ context.Context, associationID uuid.UUID, relationshipType string) (*persistence.PartyAssociationEntity, error) {
	if m.updateAssocErr != nil {
		return nil, m.updateAssocErr
	}
	if m.updateAssocResult != nil {
		return m.updateAssocResult, nil
	}
	return &persistence.PartyAssociationEntity{
		ID:               associationID,
		PartyID:          uuid.New(),
		RelatedPartyID:   uuid.New(),
		RelationshipType: relationshipType,
	}, nil
}

func (m *associationMockRepository) ListParticipants(_ context.Context, _ uuid.UUID, _ string) ([]persistence.PartyAssociationEntity, error) {
	return m.listParticipantsResult, m.listParticipantsErr
}

func (m *associationMockRepository) GetStructuringData(_ context.Context, _, _ uuid.UUID, _ string) (map[string]interface{}, error) {
	if m.getStructuringErr != nil {
		return nil, m.getStructuringErr
	}
	if m.getStructuringResult != nil {
		return m.getStructuringResult, nil
	}
	return map[string]interface{}{}, nil
}

func newAssociationMock() *associationMockRepository {
	return &associationMockRepository{
		mockRepository: *newMockRepository(),
	}
}

func TestRegisterAssociations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("registers association between two parties", func(t *testing.T) {
		mock := newAssociationMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party1, err := domain.NewParty(domain.PartyTypeOrganization, "Org1")
		require.NoError(t, err)
		party2, err := domain.NewParty(domain.PartyTypeOrganization, "Org2")
		require.NoError(t, err)
		mock.parties[party1.ID()] = party1
		mock.parties[party2.ID()] = party2

		resp, err := svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
			PartyId:          party1.ID().String(),
			RelatedPartyId:   party2.ID().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER,
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.NotEmpty(t, resp.AssociationId)
		assert.Equal(t, party1.ID().String(), resp.PartyId)
		assert.Equal(t, party2.ID().String(), resp.RelatedPartyId)
		// Default status should be ACTIVE when unspecified
		assert.Equal(t, pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE, resp.Status)
	})

	t.Run("returns InvalidArgument for invalid party ID", func(t *testing.T) {
		mock := newAssociationMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
			PartyId:          "bad-uuid",
			RelatedPartyId:   uuid.New().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns InvalidArgument for invalid related party ID", func(t *testing.T) {
		mock := newAssociationMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
			PartyId:          uuid.New().String(),
			RelatedPartyId:   "bad-uuid",
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns NotFound when party does not exist", func(t *testing.T) {
		mock := newAssociationMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
			PartyId:          uuid.New().String(),
			RelatedPartyId:   uuid.New().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns Internal when find party fails with db error", func(t *testing.T) {
		mock := newAssociationMock()
		mock.findByIDForUpdateErr = errDatabaseFailed
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
			PartyId:          uuid.New().String(),
			RelatedPartyId:   uuid.New().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("returns InvalidArgument for circular association", func(t *testing.T) {
		mock := newAssociationMock()
		mock.checkCircularResult = true
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party1, err := domain.NewParty(domain.PartyTypePerson, "A")
		require.NoError(t, err)
		party2, err := domain.NewParty(domain.PartyTypePerson, "B")
		require.NoError(t, err)
		mock.parties[party1.ID()] = party1
		mock.parties[party2.ID()] = party2

		resp, err := svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
			PartyId:          party1.ID().String(),
			RelatedPartyId:   party2.ID().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "circular")
	})

	t.Run("returns Internal when circular check fails", func(t *testing.T) {
		mock := newAssociationMock()
		mock.checkCircularErr = errors.New("db error")
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party1, err := domain.NewParty(domain.PartyTypePerson, "A")
		require.NoError(t, err)
		party2, err := domain.NewParty(domain.PartyTypePerson, "B")
		require.NoError(t, err)
		mock.parties[party1.ID()] = party1
		mock.parties[party2.ID()] = party2

		resp, err := svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
			PartyId:          party1.ID().String(),
			RelatedPartyId:   party2.ID().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("returns AlreadyExists on duplicate association", func(t *testing.T) {
		mock := newAssociationMock()
		mock.saveAssocErr = persistence.ErrAssociationExists
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party1, err := domain.NewParty(domain.PartyTypePerson, "A")
		require.NoError(t, err)
		party2, err := domain.NewParty(domain.PartyTypePerson, "B")
		require.NoError(t, err)
		mock.parties[party1.ID()] = party1
		mock.parties[party2.ID()] = party2

		resp, err := svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
			PartyId:          party1.ID().String(),
			RelatedPartyId:   party2.ID().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})

	t.Run("returns Internal on save error", func(t *testing.T) {
		mock := newAssociationMock()
		mock.saveAssocErr = errors.New("unexpected")
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party1, err := domain.NewParty(domain.PartyTypePerson, "A")
		require.NoError(t, err)
		party2, err := domain.NewParty(domain.PartyTypePerson, "B")
		require.NoError(t, err)
		mock.parties[party1.ID()] = party1
		mock.parties[party2.ID()] = party2

		resp, err := svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
			PartyId:          party1.ID().String(),
			RelatedPartyId:   party2.ID().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestUpdateAssociations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("updates association successfully", func(t *testing.T) {
		mock := newAssociationMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		assocID := uuid.New()
		resp, err := svc.UpdateAssociations(ctx, &pb.UpdateAssociationsRequest{
			AssociationId:    assocID.String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_GUARANTOR,
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, assocID.String(), resp.AssociationId)
		assert.NotNil(t, resp.UpdatedAt)
	})

	t.Run("returns InvalidArgument for invalid association ID", func(t *testing.T) {
		mock := newAssociationMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateAssociations(ctx, &pb.UpdateAssociationsRequest{
			AssociationId:    "bad",
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns NotFound when association not found", func(t *testing.T) {
		mock := newAssociationMock()
		mock.updateAssocErr = gorm.ErrRecordNotFound
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateAssociations(ctx, &pb.UpdateAssociationsRequest{
			AssociationId:    uuid.New().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns Internal on update error", func(t *testing.T) {
		mock := newAssociationMock()
		mock.updateAssocErr = errors.New("db error")
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateAssociations(ctx, &pb.UpdateAssociationsRequest{
			AssociationId:    uuid.New().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestRetrieveAssociations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("retrieves associations successfully", func(t *testing.T) {
		mock := newAssociationMock()
		mock.findAssocResult = []persistence.PartyAssociationEntity{
			{
				ID:               uuid.New(),
				PartyID:          uuid.New(),
				RelatedPartyID:   uuid.New(),
				RelationshipType: "SPOUSE",
				Status:           "ACTIVE",
			},
		}
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveAssociations(ctx, &pb.RetrieveAssociationsRequest{
			PartyId: uuid.New().String(),
		})
		require.NoError(t, err)
		require.Len(t, resp.Associations, 1)
	})

	t.Run("returns InvalidArgument for invalid party ID", func(t *testing.T) {
		mock := newAssociationMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveAssociations(ctx, &pb.RetrieveAssociationsRequest{
			PartyId: "bad",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns Internal on repository error", func(t *testing.T) {
		mock := newAssociationMock()
		mock.findAssocErr = errDatabaseFailed
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveAssociations(ctx, &pb.RetrieveAssociationsRequest{
			PartyId: uuid.New().String(),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestListParticipants_Unit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns InvalidArgument for invalid relationship type", func(t *testing.T) {
		mock := newAssociationMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.ListParticipants(ctx, &pb.ListParticipantsRequest{
			OrgPartyId:       uuid.New().String(),
			RelationshipType: pb.RelationshipType(999),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns Internal on repository error", func(t *testing.T) {
		mock := newAssociationMock()
		mock.listParticipantsErr = errDatabaseFailed
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.ListParticipants(ctx, &pb.ListParticipantsRequest{
			OrgPartyId:       uuid.New().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestGetStructuringData_Unit(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("returns InvalidArgument for invalid relationship type", func(t *testing.T) {
		mock := newAssociationMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.GetStructuringData(ctx, &pb.GetStructuringDataRequest{
			PartyId:          uuid.New().String(),
			OrgPartyId:       uuid.New().String(),
			RelationshipType: pb.RelationshipType(999),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns Internal on repository error", func(t *testing.T) {
		mock := newAssociationMock()
		mock.getStructuringErr = errDatabaseFailed
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.GetStructuringData(ctx, &pb.GetStructuringDataRequest{
			PartyId:          uuid.New().String(),
			OrgPartyId:       uuid.New().String(),
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}
