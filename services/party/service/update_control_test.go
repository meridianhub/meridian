package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

func TestUpdateParty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("updates display name without field mask", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Alice Smith")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
			PartyId:     party.ID().String(),
			DisplayName: "Ali",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "Ali", resp.Party.DisplayName)
	})

	t.Run("updates display name with field mask", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Bob Jones")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
			PartyId:     party.ID().String(),
			DisplayName: "Bobby",
			UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{"display_name"}},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "Bobby", resp.Party.DisplayName)
	})

	t.Run("updates attributes with field mask", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypeOrganization, "Acme Corp")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
			PartyId: party.ID().String(),
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "tier", Value: "gold"},
			},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"attributes"}},
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Len(t, resp.Party.Attributes, 1)
		assert.Equal(t, "tier", resp.Party.Attributes[0].Key)
		assert.Equal(t, "gold", resp.Party.Attributes[0].Value)
	})

	t.Run("updates attributes without field mask", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypeOrganization, "Corp Ltd")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
			PartyId: party.ID().String(),
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "region", Value: "EU"},
			},
		})
		require.NoError(t, err)
		require.Len(t, resp.Party.Attributes, 1)
		assert.Equal(t, "region", resp.Party.Attributes[0].Key)
	})

	t.Run("returns InvalidArgument for invalid party ID", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
			PartyId: "not-a-uuid",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns NotFound for non-existent party", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
			PartyId:     uuid.New().String(),
			DisplayName: "Test",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns Internal on find error", func(t *testing.T) {
		mock := newMockRepository()
		mock.findByIDForUpdateErr = errDatabaseFailed
		svc := newTestService(mock)

		resp, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
			PartyId: uuid.New().String(),
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("returns Aborted on version conflict check", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Carol")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
			PartyId: party.ID().String(),
			Version: 999, // Wrong version
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Aborted, st.Code())
	})

	t.Run("returns InvalidArgument for unsupported field mask path", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Dave")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
			PartyId:    party.ID().String(),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"unsupported_field"}},
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns Internal on save error", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Eve")
		require.NoError(t, err)
		mock.parties[party.ID()] = party
		mock.saveErr = errDatabaseFailed

		resp, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
			PartyId:     party.ID().String(),
			DisplayName: "Evie",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("returns Aborted on version conflict during save", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Frank")
		require.NoError(t, err)
		mock.parties[party.ID()] = party
		mock.saveErr = persistence.ErrVersionConflict

		resp, err := svc.UpdateParty(ctx, &pb.UpdatePartyRequest{
			PartyId:     party.ID().String(),
			DisplayName: "Frankie",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Aborted, st.Code())
	})
}

func TestControlParty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("suspends active party", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Alice Smith")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       party.ID().String(),
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
			Reason:        "suspicious activity",
			ActorId:       "admin-123",
		})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, pb.PartyStatus_PARTY_STATUS_SUSPENDED, resp.Party.Status)
		assert.NotNil(t, resp.ActionTimestamp)
		assert.True(t, resp.ActionTimestamp.AsTime().Before(time.Now().Add(time.Second)))
	})

	t.Run("restricts active party", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Bob")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		resp, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       party.ID().String(),
			ControlAction: pb.ControlAction_CONTROL_ACTION_RESTRICT,
			Reason:        "under review",
		})
		require.NoError(t, err)
		assert.Equal(t, pb.PartyStatus_PARTY_STATUS_RESTRICTED, resp.Party.Status)
	})

	t.Run("terminates suspended party", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Carol")
		require.NoError(t, err)
		require.NoError(t, party.ControlParty(domain.ControlActionSuspend, "test"))
		mock.parties[party.ID()] = party

		resp, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       party.ID().String(),
			ControlAction: pb.ControlAction_CONTROL_ACTION_TERMINATE,
			Reason:        "account closure",
		})
		require.NoError(t, err)
		assert.Equal(t, pb.PartyStatus_PARTY_STATUS_TERMINATED, resp.Party.Status)
	})

	t.Run("reactivates suspended party", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Dave")
		require.NoError(t, err)
		require.NoError(t, party.ControlParty(domain.ControlActionSuspend, "test"))
		mock.parties[party.ID()] = party

		resp, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       party.ID().String(),
			ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
			Reason:        "cleared",
		})
		require.NoError(t, err)
		assert.Equal(t, pb.PartyStatus_PARTY_STATUS_ACTIVE, resp.Party.Status)
	})

	t.Run("returns InvalidArgument for invalid party ID", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       "bad-uuid",
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns InvalidArgument for unspecified action", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       uuid.New().String(),
			ControlAction: pb.ControlAction_CONTROL_ACTION_UNSPECIFIED,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns NotFound for non-existent party", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       uuid.New().String(),
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns Internal on find error", func(t *testing.T) {
		mock := newMockRepository()
		mock.findByIDForUpdateErr = errDatabaseFailed
		svc := newTestService(mock)

		resp, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       uuid.New().String(),
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("returns FailedPrecondition for invalid transition", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Terminated")
		require.NoError(t, err)
		require.NoError(t, party.ControlParty(domain.ControlActionSuspend, "test"))
		require.NoError(t, party.ControlParty(domain.ControlActionTerminate, "test"))
		mock.parties[party.ID()] = party

		resp, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       party.ID().String(),
			ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})

	t.Run("returns Internal on save error", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Grace")
		require.NoError(t, err)
		mock.parties[party.ID()] = party
		mock.saveErr = errDatabaseFailed

		resp, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       party.ID().String(),
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
			Reason:        "test",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("returns Aborted on version conflict save", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Heidi")
		require.NoError(t, err)
		mock.parties[party.ID()] = party
		mock.saveErr = persistence.ErrVersionConflict

		resp, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       party.ID().String(),
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
			Reason:        "test",
		})
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Aborted, st.Code())
	})
}

func TestApplyFieldUpdate(t *testing.T) {
	t.Parallel()

	t.Run("updates display_name", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Alice")
		require.NoError(t, err)

		err = svc.applyFieldUpdate(party, "display_name", &pb.UpdatePartyRequest{
			DisplayName: "Ali",
		})
		require.NoError(t, err)
		assert.Equal(t, "Ali", party.DisplayName())
	})

	t.Run("no-op for empty display_name", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Alice")
		require.NoError(t, err)

		err = svc.applyFieldUpdate(party, "display_name", &pb.UpdatePartyRequest{
			DisplayName: "",
		})
		require.NoError(t, err)
		assert.Empty(t, party.DisplayName())
	})

	t.Run("updates attributes", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypeOrganization, "Corp")
		require.NoError(t, err)

		err = svc.applyFieldUpdate(party, "attributes", &pb.UpdatePartyRequest{
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "k", Value: "v"},
			},
		})
		require.NoError(t, err)
		assert.Len(t, party.Attributes(), 1)
	})

	t.Run("returns error for unsupported field", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Alice")
		require.NoError(t, err)

		err = svc.applyFieldUpdate(party, "legal_name", &pb.UpdatePartyRequest{})
		assert.ErrorIs(t, err, ErrUnsupportedFieldUpdate)
	})
}

func TestWithBuilders(t *testing.T) {
	t.Parallel()

	t.Run("WithPaymentMethodRepository", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)
		result := svc.WithPaymentMethodRepository(nil)
		assert.Same(t, svc, result)
	})

	t.Run("WithVerificationProvider", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)
		result := svc.WithVerificationProvider(nil)
		assert.Same(t, svc, result)
	})

	t.Run("WithAttributeValidator", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)
		result := svc.WithAttributeValidator(nil)
		assert.Same(t, svc, result)
	})

	t.Run("WithOutboxPublisher", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)
		result := svc.WithOutboxPublisher(nil, nil)
		assert.Same(t, svc, result)
	})
}

func TestSavePartyWithEvent_NoOutbox(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := newMockRepository()
	svc := newTestService(mock)

	party, err := domain.NewParty(domain.PartyTypePerson, "Test")
	require.NoError(t, err)

	// Without outbox, should fall back to plain save
	err = svc.savePartyWithEvent(ctx, party, nil)
	require.NoError(t, err)
	assert.Contains(t, mock.parties, party.ID())
}
