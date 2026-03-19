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

// referenceMockRepository extends mockRepository with configurable reference behavior.
type referenceMockRepository struct {
	mockRepository
	saveRefsErr    error
	findRefsResult []persistence.PartyReferenceEntity
	findRefsErr    error
}

func (m *referenceMockRepository) SaveReferences(_ context.Context, _ uuid.UUID, _ []persistence.ReferenceInput) error {
	return m.saveRefsErr
}

func (m *referenceMockRepository) FindReferences(_ context.Context, _ uuid.UUID) ([]persistence.PartyReferenceEntity, error) {
	return m.findRefsResult, m.findRefsErr
}

func newReferenceMock() *referenceMockRepository {
	return &referenceMockRepository{
		mockRepository: *newMockRepository(),
	}
}

func TestUpdateReference(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("saves government ID and tax reference", func(t *testing.T) {
		mock := newReferenceMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party, err := domain.NewParty(domain.PartyTypePerson, "Alice")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateReference(ctx, &pb.UpdateReferenceRequest{
			PartyId:          party.ID().String(),
			GovernmentId:     "AB123456",
			TaxReference:     "UTR123456789",
			IssuingAuthority: "HMRC",
			ExpiryDate:       "2030-12-31",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "AB123456", resp.GovernmentId)
		assert.Equal(t, "UTR123456789", resp.TaxReference)
		assert.Equal(t, "HMRC", resp.IssuingAuthority)
		assert.Equal(t, "2030-12-31", resp.ExpiryDate)
		assert.NotNil(t, resp.UpdatedAt)
	})

	t.Run("saves government ID only", func(t *testing.T) {
		mock := newReferenceMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party, err := domain.NewParty(domain.PartyTypePerson, "Bob")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateReference(ctx, &pb.UpdateReferenceRequest{
			PartyId:      party.ID().String(),
			GovernmentId: "CD987654",
		})
		require.NoError(t, err)
		assert.Equal(t, "CD987654", resp.GovernmentId)
		assert.Empty(t, resp.TaxReference)
	})

	t.Run("no-op when no references provided", func(t *testing.T) {
		mock := newReferenceMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party, err := domain.NewParty(domain.PartyTypePerson, "Carol")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateReference(ctx, &pb.UpdateReferenceRequest{
			PartyId: party.ID().String(),
		})
		require.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("returns InvalidArgument for invalid party ID", func(t *testing.T) {
		mock := newReferenceMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateReference(ctx, &pb.UpdateReferenceRequest{
			PartyId: "bad",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns NotFound for non-existent party", func(t *testing.T) {
		mock := newReferenceMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateReference(ctx, &pb.UpdateReferenceRequest{
			PartyId:      uuid.New().String(),
			GovernmentId: "ABC",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns Internal on find error", func(t *testing.T) {
		mock := newReferenceMock()
		mock.findByIDForUpdateErr = errDatabaseFailed
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.UpdateReference(ctx, &pb.UpdateReferenceRequest{
			PartyId: uuid.New().String(),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("returns InvalidArgument for bad expiry date", func(t *testing.T) {
		mock := newReferenceMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party, err := domain.NewParty(domain.PartyTypePerson, "Dave")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateReference(ctx, &pb.UpdateReferenceRequest{
			PartyId:      party.ID().String(),
			GovernmentId: "ABC",
			ExpiryDate:   "31/12/2030", // Bad format
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "expiry_date")
	})

	t.Run("returns Internal on save error", func(t *testing.T) {
		mock := newReferenceMock()
		mock.saveRefsErr = errors.New("write failed")
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		party, err := domain.NewParty(domain.PartyTypePerson, "Eve")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateReference(ctx, &pb.UpdateReferenceRequest{
			PartyId:      party.ID().String(),
			GovernmentId: "XY123",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestRetrieveReference(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("retrieves government ID and tax reference", func(t *testing.T) {
		mock := newReferenceMock()
		issuingAuth := "HMRC"
		expiryDate := time.Date(2030, 12, 31, 0, 0, 0, 0, time.UTC)
		mock.findRefsResult = []persistence.PartyReferenceEntity{
			{
				ReferenceType:    "GOVERNMENT_ID",
				ReferenceValue:   "AB123456",
				IssuingAuthority: &issuingAuth,
				ExpiryDate:       &expiryDate,
				CreatedAt:        time.Now(),
			},
			{
				ReferenceType:  "TAX_REFERENCE",
				ReferenceValue: "UTR123456789",
				CreatedAt:      time.Now(),
			},
		}
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveReference(ctx, &pb.RetrieveReferenceRequest{
			PartyId: uuid.New().String(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "AB123456", resp.GovernmentId)
		assert.Equal(t, "HMRC", resp.IssuingAuthority)
		assert.Equal(t, "2030-12-31", resp.ExpiryDate)
		assert.Equal(t, "UTR123456789", resp.TaxReference)
		assert.NotNil(t, resp.UpdatedAt)
	})

	t.Run("retrieves empty when no references", func(t *testing.T) {
		mock := newReferenceMock()
		mock.findRefsResult = []persistence.PartyReferenceEntity{}
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveReference(ctx, &pb.RetrieveReferenceRequest{
			PartyId: uuid.New().String(),
		})
		require.NoError(t, err)
		assert.Empty(t, resp.GovernmentId)
		assert.Empty(t, resp.TaxReference)
	})

	t.Run("handles government ID without optional fields", func(t *testing.T) {
		mock := newReferenceMock()
		mock.findRefsResult = []persistence.PartyReferenceEntity{
			{
				ReferenceType:  "GOVERNMENT_ID",
				ReferenceValue: "XY789",
				CreatedAt:      time.Now(),
			},
		}
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveReference(ctx, &pb.RetrieveReferenceRequest{
			PartyId: uuid.New().String(),
		})
		require.NoError(t, err)
		assert.Equal(t, "XY789", resp.GovernmentId)
		assert.Empty(t, resp.IssuingAuthority)
		assert.Empty(t, resp.ExpiryDate)
	})

	t.Run("returns InvalidArgument for invalid party ID", func(t *testing.T) {
		mock := newReferenceMock()
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveReference(ctx, &pb.RetrieveReferenceRequest{
			PartyId: "nope",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns Internal on repository error", func(t *testing.T) {
		mock := newReferenceMock()
		mock.findRefsErr = errDatabaseFailed
		svc := newTestService(&mock.mockRepository)
		svc.repo = mock

		resp, err := svc.RetrieveReference(ctx, &pb.RetrieveReferenceRequest{
			PartyId: uuid.New().String(),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}
