//nolint:misspell // Proto uses British spelling for ORGANISATION
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

// Test errors
var (
	errDatabaseFailed  = errors.New("database connection failed")
	errDatabaseTimeout = errors.New("database timeout")
)

// mockRepository implements Repository interface for testing
type mockRepository struct {
	parties              map[uuid.UUID]*domain.Party
	externalRefs         map[string]*domain.Party
	saveErr              error
	findByIDErr          error
	findByExternalRefErr error
}

func newMockRepository() *mockRepository {
	return &mockRepository{
		parties:      make(map[uuid.UUID]*domain.Party),
		externalRefs: make(map[string]*domain.Party),
	}
}

func (m *mockRepository) Save(_ context.Context, party *domain.Party) error {
	if m.saveErr != nil {
		return m.saveErr
	}
	m.parties[party.ID()] = party
	if party.ExternalReference() != "" {
		key := party.ExternalReference() + ":" + string(party.ExternalReferenceType())
		m.externalRefs[key] = party
	}
	return nil
}

func (m *mockRepository) FindByID(id uuid.UUID) (*domain.Party, error) {
	if m.findByIDErr != nil {
		return nil, m.findByIDErr
	}
	party, ok := m.parties[id]
	if !ok {
		return nil, persistence.ErrPartyNotFound
	}
	return party, nil
}

func (m *mockRepository) FindByExternalReference(ref, refType string) (*domain.Party, error) {
	if m.findByExternalRefErr != nil {
		return nil, m.findByExternalRefErr
	}
	key := ref + ":" + refType
	party, ok := m.externalRefs[key]
	if !ok {
		return nil, persistence.ErrPartyNotFound
	}
	return party, nil
}

func newTestService(mock *mockRepository) *Service {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, _ := NewService(mock, logger)
	return svc
}

func TestNewService(t *testing.T) {
	t.Run("returns error when repository is nil", func(t *testing.T) {
		svc, err := NewService(nil, nil)
		assert.Nil(t, svc)
		assert.ErrorIs(t, err, ErrRepositoryNil)
	})

	t.Run("creates service with default logger when nil", func(t *testing.T) {
		mock := newMockRepository()
		svc, err := NewService(mock, nil)
		require.NoError(t, err)
		assert.NotNil(t, svc)
		assert.NotNil(t, svc.logger)
	})
}

func TestRegisterParty(t *testing.T) {
	ctx := context.Background()

	t.Run("registers person party successfully", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		req := &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_PERSON,
			LegalName: "John Smith",
		}

		resp, err := svc.RegisterParty(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Party)

		assert.NotEmpty(t, resp.Party.PartyId)
		assert.Equal(t, pb.PartyType_PARTY_TYPE_PERSON, resp.Party.PartyType)
		assert.Equal(t, "John Smith", resp.Party.LegalName)
		assert.Equal(t, pb.PartyStatus_PARTY_STATUS_ACTIVE, resp.Party.Status)
		assert.Equal(t, int32(1), resp.Party.Version)
	})

	t.Run("registers organization party successfully", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		req := &pb.RegisterPartyRequest{
			PartyType:   pb.PartyType_PARTY_TYPE_ORGANISATION, //nolint:misspell // Proto uses British spelling
			LegalName:   "Acme Corporation Ltd",
			DisplayName: "Acme Corp",
		}

		resp, err := svc.RegisterParty(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Party)

		assert.Equal(t, pb.PartyType_PARTY_TYPE_ORGANISATION, resp.Party.PartyType) //nolint:misspell // Proto uses British spelling
		assert.Equal(t, "Acme Corporation Ltd", resp.Party.LegalName)
		assert.Equal(t, "Acme Corp", resp.Party.DisplayName)
	})

	t.Run("registers party with external reference", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		req := &pb.RegisterPartyRequest{
			PartyType:             pb.PartyType_PARTY_TYPE_ORGANISATION, //nolint:misspell // Proto uses British spelling
			LegalName:             "Tech Company Ltd",
			ExternalReference:     "12345678",
			ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
		}

		resp, err := svc.RegisterParty(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		assert.Equal(t, "12345678", resp.Party.ExternalReference)
		assert.Equal(t, pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE, resp.Party.ExternalReferenceType)
	})

	t.Run("returns InvalidArgument for unspecified party type", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		req := &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_UNSPECIFIED,
			LegalName: "Test Party",
		}

		resp, err := svc.RegisterParty(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns InvalidArgument for empty legal name", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		req := &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_PERSON,
			LegalName: "",
		}

		resp, err := svc.RegisterParty(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns AlreadyExists for duplicate external reference", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		// Create existing party with external reference
		existingParty, err := domain.NewParty(domain.PartyTypeOrganization, "Existing Corp")
		require.NoError(t, err)
		err = existingParty.SetExternalReference("12345678", domain.ExternalReferenceTypeCompaniesHouse)
		require.NoError(t, err)
		mock.parties[existingParty.ID()] = existingParty
		mock.externalRefs["12345678:COMPANIES_HOUSE"] = existingParty

		// Attempt duplicate registration
		req := &pb.RegisterPartyRequest{
			PartyType:             pb.PartyType_PARTY_TYPE_ORGANISATION, //nolint:misspell // Proto uses British spelling
			LegalName:             "New Corp",
			ExternalReference:     "12345678",
			ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
		}

		resp, err := svc.RegisterParty(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})

	t.Run("returns InvalidArgument for external reference without type", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		req := &pb.RegisterPartyRequest{
			PartyType:             pb.PartyType_PARTY_TYPE_ORGANISATION, //nolint:misspell // Proto uses British spelling
			LegalName:             "Test Corp",
			ExternalReference:     "12345678",
			ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED,
		}

		resp, err := svc.RegisterParty(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns Internal on repository save error", func(t *testing.T) {
		mock := newMockRepository()
		mock.saveErr = errDatabaseFailed
		svc := newTestService(mock)

		req := &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_PERSON,
			LegalName: "Test Person",
		}

		resp, err := svc.RegisterParty(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("returns AlreadyExists on race condition save error", func(t *testing.T) {
		mock := newMockRepository()
		mock.saveErr = persistence.ErrPartyExists
		svc := newTestService(mock)

		req := &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_PERSON,
			LegalName: "Test Person",
		}

		resp, err := svc.RegisterParty(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})
}

func TestRetrieveParty(t *testing.T) {
	ctx := context.Background()

	t.Run("retrieves existing party successfully", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		// Create and store a party
		party, err := domain.NewParty(domain.PartyTypePerson, "Jane Doe")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		req := &pb.RetrievePartyRequest{
			PartyId: party.ID().String(),
		}

		resp, err := svc.RetrieveParty(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Party)

		assert.Equal(t, party.ID().String(), resp.Party.PartyId)
		assert.Equal(t, "Jane Doe", resp.Party.LegalName)
		assert.Equal(t, pb.PartyType_PARTY_TYPE_PERSON, resp.Party.PartyType)
	})

	t.Run("returns NotFound for non-existent party", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		req := &pb.RetrievePartyRequest{
			PartyId: uuid.New().String(),
		}

		resp, err := svc.RetrieveParty(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})

	t.Run("returns InvalidArgument for invalid UUID format", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		req := &pb.RetrievePartyRequest{
			PartyId: "not-a-uuid",
		}

		resp, err := svc.RetrieveParty(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns Internal on repository error", func(t *testing.T) {
		mock := newMockRepository()
		mock.findByIDErr = errDatabaseTimeout
		svc := newTestService(mock)

		req := &pb.RetrievePartyRequest{
			PartyId: uuid.New().String(),
		}

		resp, err := svc.RetrieveParty(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}

func TestPartyTypeConversions(t *testing.T) {
	t.Run("protoToPartyType", func(t *testing.T) {
		tests := []struct {
			input    pb.PartyType
			expected domain.PartyType
			wantErr  bool
		}{
			{pb.PartyType_PARTY_TYPE_PERSON, domain.PartyTypePerson, false},
			{pb.PartyType_PARTY_TYPE_ORGANISATION, domain.PartyTypeOrganization, false},
			{pb.PartyType_PARTY_TYPE_UNSPECIFIED, "", true},
		}

		for _, tt := range tests {
			result, err := protoToPartyType(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		}
	})

	t.Run("partyTypeToProto", func(t *testing.T) {
		tests := []struct {
			input    domain.PartyType
			expected pb.PartyType
		}{
			{domain.PartyTypePerson, pb.PartyType_PARTY_TYPE_PERSON},
			{domain.PartyTypeOrganization, pb.PartyType_PARTY_TYPE_ORGANISATION},
			{"UNKNOWN", pb.PartyType_PARTY_TYPE_UNSPECIFIED},
		}

		for _, tt := range tests {
			result := partyTypeToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		}
	})
}

func TestPartyStatusConversions(t *testing.T) {
	tests := []struct {
		input    domain.PartyStatus
		expected pb.PartyStatus
	}{
		{domain.PartyStatusActive, pb.PartyStatus_PARTY_STATUS_ACTIVE},
		{domain.PartyStatusRestricted, pb.PartyStatus_PARTY_STATUS_RESTRICTED},
		{domain.PartyStatusTerminated, pb.PartyStatus_PARTY_STATUS_TERMINATED},
		{"UNKNOWN", pb.PartyStatus_PARTY_STATUS_UNSPECIFIED},
	}

	for _, tt := range tests {
		result := partyStatusToProto(tt.input)
		assert.Equal(t, tt.expected, result)
	}
}

func TestExternalRefTypeConversions(t *testing.T) {
	t.Run("protoToExternalRefType", func(t *testing.T) {
		tests := []struct {
			input    pb.ExternalReferenceType
			expected domain.ExternalReferenceType
			wantErr  bool
		}{
			{pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE, domain.ExternalReferenceTypeCompaniesHouse, false},
			{pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_NATIONAL_ID, domain.ExternalReferenceTypeNationalID, false},
			{pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI, domain.ExternalReferenceTypeLEI, false},
			{pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_TAX_ID, domain.ExternalReferenceTypeTaxID, false},
			{pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED, "", true},
		}

		for _, tt := range tests {
			result, err := protoToExternalRefType(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		}
	})

	t.Run("externalRefTypeToProto", func(t *testing.T) {
		tests := []struct {
			input    domain.ExternalReferenceType
			expected pb.ExternalReferenceType
		}{
			{domain.ExternalReferenceTypeCompaniesHouse, pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE},
			{domain.ExternalReferenceTypeNationalID, pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_NATIONAL_ID},
			{domain.ExternalReferenceTypeLEI, pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI},
			{domain.ExternalReferenceTypeTaxID, pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_TAX_ID},
			{"UNKNOWN", pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED},
		}

		for _, tt := range tests {
			result := externalRefTypeToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		}
	})
}

func TestDomainToProto(t *testing.T) {
	t.Run("converts party with all fields", func(t *testing.T) {
		party, err := domain.NewParty(domain.PartyTypeOrganization, "Test Corp Ltd")
		require.NoError(t, err)

		err = party.SetDisplayName("Test Corp")
		require.NoError(t, err)

		err = party.SetExternalReference("12345678", domain.ExternalReferenceTypeCompaniesHouse)
		require.NoError(t, err)

		protoParty := domainToProto(party)

		assert.Equal(t, party.ID().String(), protoParty.PartyId)
		assert.Equal(t, pb.PartyType_PARTY_TYPE_ORGANISATION, protoParty.PartyType)
		assert.Equal(t, "Test Corp Ltd", protoParty.LegalName)
		assert.Equal(t, "Test Corp", protoParty.DisplayName)
		assert.Equal(t, pb.PartyStatus_PARTY_STATUS_ACTIVE, protoParty.Status)
		assert.Equal(t, "12345678", protoParty.ExternalReference)
		assert.Equal(t, pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE, protoParty.ExternalReferenceType)
		// Version incremented for SetDisplayName and SetExternalReference
		assert.Equal(t, int32(3), protoParty.Version)
		assert.NotNil(t, protoParty.CreatedAt)
		assert.NotNil(t, protoParty.UpdatedAt)
	})

	t.Run("converts party with minimal fields", func(t *testing.T) {
		party, err := domain.NewParty(domain.PartyTypePerson, "Jane Doe")
		require.NoError(t, err)

		protoParty := domainToProto(party)

		assert.Equal(t, party.ID().String(), protoParty.PartyId)
		assert.Equal(t, pb.PartyType_PARTY_TYPE_PERSON, protoParty.PartyType)
		assert.Equal(t, "Jane Doe", protoParty.LegalName)
		assert.Empty(t, protoParty.DisplayName)
		assert.Equal(t, pb.PartyStatus_PARTY_STATUS_ACTIVE, protoParty.Status)
		assert.Empty(t, protoParty.ExternalReference)
		assert.Equal(t, pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED, protoParty.ExternalReferenceType)
		assert.Equal(t, int32(1), protoParty.Version)
	})
}
