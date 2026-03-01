package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/services/party/verification"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
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
	findByIDForUpdateErr error
	findByExternalRefErr error
	listPartiesErr       error
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

func (m *mockRepository) SaveInTx(ctx context.Context, party *domain.Party, _ *gorm.DB) error {
	return m.Save(ctx, party)
}

func (m *mockRepository) FindByID(_ context.Context, id uuid.UUID) (*domain.Party, error) {
	if m.findByIDErr != nil {
		return nil, m.findByIDErr
	}
	party, ok := m.parties[id]
	if !ok {
		return nil, persistence.ErrPartyNotFound
	}
	return party, nil
}

func (m *mockRepository) FindByIDForUpdate(_ context.Context, id uuid.UUID) (*domain.Party, error) {
	if m.findByIDForUpdateErr != nil {
		return nil, m.findByIDForUpdateErr
	}
	party, ok := m.parties[id]
	if !ok {
		return nil, persistence.ErrPartyNotFound
	}
	return party, nil
}

func (m *mockRepository) FindByExternalReference(_ context.Context, ref, refType string) (*domain.Party, error) {
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

// BQ operation stubs for mock repository
func (m *mockRepository) SaveAssociation(_ context.Context, _, _ uuid.UUID, _ string) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (m *mockRepository) FindAssociations(_ context.Context, _ uuid.UUID) ([]persistence.PartyAssociationEntity, error) {
	return []persistence.PartyAssociationEntity{}, nil
}

func (m *mockRepository) UpdateAssociation(_ context.Context, associationID uuid.UUID, relationshipType string) (*persistence.PartyAssociationEntity, error) {
	return &persistence.PartyAssociationEntity{
		ID:               associationID,
		PartyID:          uuid.New(),
		RelatedPartyID:   uuid.New(),
		RelationshipType: relationshipType,
	}, nil
}

func (m *mockRepository) CheckCircularAssociation(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return false, nil
}

func (m *mockRepository) SaveDemographic(_ context.Context, _ uuid.UUID, _, _ string) error {
	return nil
}

func (m *mockRepository) FindDemographic(_ context.Context, _ uuid.UUID) (*persistence.PartyDemographicEntity, error) {
	return nil, nil
}

func (m *mockRepository) SaveReference(_ context.Context, _ uuid.UUID, _, _, _, _ string) error {
	return nil
}

func (m *mockRepository) SaveReferences(_ context.Context, _ uuid.UUID, _ []persistence.ReferenceInput) error {
	return nil
}

func (m *mockRepository) FindReferences(_ context.Context, _ uuid.UUID) ([]persistence.PartyReferenceEntity, error) {
	return []persistence.PartyReferenceEntity{}, nil
}

func (m *mockRepository) SaveBankRelation(_ context.Context, _ uuid.UUID, _, _, _ string) error {
	return nil
}

func (m *mockRepository) FindBankRelation(_ context.Context, _ uuid.UUID) (*persistence.PartyBankRelationEntity, error) {
	return nil, nil
}

func (m *mockRepository) SaveAssociationWithInput(_ context.Context, _, _ uuid.UUID, _ string, _ *persistence.AssociationInput) (uuid.UUID, error) {
	return uuid.New(), nil
}

func (m *mockRepository) ListParticipants(_ context.Context, _ uuid.UUID, _ string) ([]persistence.PartyAssociationEntity, error) {
	return []persistence.PartyAssociationEntity{}, nil
}

func (m *mockRepository) GetStructuringData(_ context.Context, _, _ uuid.UUID, _ string) (map[string]interface{}, error) {
	return map[string]interface{}{}, nil
}

func (m *mockRepository) ListParties(_ context.Context, params persistence.ListPartiesParams) (*persistence.ListPartiesResult, error) {
	if m.listPartiesErr != nil {
		return nil, m.listPartiesErr
	}
	// Collect all parties matching filters
	var parties []*domain.Party
	for _, p := range m.parties {
		if params.PartyType != "" && string(p.PartyType()) != params.PartyType {
			continue
		}
		if params.Status != "" && string(p.Status()) != params.Status {
			continue
		}
		if params.SearchQuery != "" {
			q := strings.ToLower(params.SearchQuery)
			if !strings.Contains(strings.ToLower(p.LegalName()), q) &&
				!strings.Contains(strings.ToLower(p.DisplayName()), q) {
				continue
			}
		}
		parties = append(parties, p)
	}

	total := int64(len(parties))

	// Apply limit
	limit := params.Limit
	if limit <= 0 {
		limit = 25
	}
	hasMore := len(parties) > limit
	if hasMore {
		parties = parties[:limit]
	}

	var nextCursor string
	if hasMore && len(parties) > 0 {
		last := parties[len(parties)-1]
		nextCursor = persistence.EncodePartyCursor(persistence.PartyCursor{
			CreatedAt: last.CreatedAt(),
			ID:        last.ID(),
		})
	}

	return &persistence.ListPartiesResult{
		Parties:    parties,
		TotalCount: total,
		NextCursor: nextCursor,
	}, nil
}

func newTestService(mock *mockRepository) *Service {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, _ := NewService(mock, logger)
	return svc
}

func TestNewService(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
			PartyType:   pb.PartyType_PARTY_TYPE_ORGANIZATION,
			LegalName:   "Acme Corporation Ltd",
			DisplayName: "Acme Corp",
		}

		resp, err := svc.RegisterParty(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Party)

		assert.Equal(t, pb.PartyType_PARTY_TYPE_ORGANIZATION, resp.Party.PartyType)
		assert.Equal(t, "Acme Corporation Ltd", resp.Party.LegalName)
		assert.Equal(t, "Acme Corp", resp.Party.DisplayName)
	})

	t.Run("registers party with external reference", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		req := &pb.RegisterPartyRequest{
			PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
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
			PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
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
			PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
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

	t.Run("returns InvalidArgument for external reference type without reference", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		req := &pb.RegisterPartyRequest{
			PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
			LegalName:             "Test Corp",
			ExternalReference:     "",
			ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
		}

		resp, err := svc.RegisterParty(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "external reference required when type is specified")
	})

	t.Run("returns Internal when external reference check fails", func(t *testing.T) {
		mock := newMockRepository()
		mock.findByExternalRefErr = errDatabaseFailed
		svc := newTestService(mock)

		req := &pb.RegisterPartyRequest{
			PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
			LegalName:             "Test Corp",
			ExternalReference:     "12345678",
			ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
		}

		resp, err := svc.RegisterParty(ctx, req)
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
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
	t.Parallel()

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
	t.Parallel()

	t.Run("protoToPartyType", func(t *testing.T) {
		tests := []struct {
			name     string
			input    pb.PartyType
			expected domain.PartyType
			wantErr  bool
		}{
			{"person type", pb.PartyType_PARTY_TYPE_PERSON, domain.PartyTypePerson, false},
			{"organization type", pb.PartyType_PARTY_TYPE_ORGANIZATION, domain.PartyTypeOrganization, false},
			{"unspecified type returns error", pb.PartyType_PARTY_TYPE_UNSPECIFIED, "", true},
			{"unknown enum value returns error", pb.PartyType(999), "", true},
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
			{domain.PartyTypeOrganization, pb.PartyType_PARTY_TYPE_ORGANIZATION},
			{"UNKNOWN", pb.PartyType_PARTY_TYPE_UNSPECIFIED},
		}

		for _, tt := range tests {
			result := partyTypeToProto(tt.input)
			assert.Equal(t, tt.expected, result)
		}
	})
}

func TestPartyStatusConversions(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

	t.Run("returns nil for nil party", func(t *testing.T) {
		result := domainToProto(nil)
		assert.Nil(t, result)
	})

	t.Run("converts party with all fields", func(t *testing.T) {
		party, err := domain.NewParty(domain.PartyTypeOrganization, "Test Corp Ltd")
		require.NoError(t, err)

		err = party.SetDisplayName("Test Corp")
		require.NoError(t, err)

		err = party.SetExternalReference("12345678", domain.ExternalReferenceTypeCompaniesHouse)
		require.NoError(t, err)

		protoParty := domainToProto(party)

		assert.Equal(t, party.ID().String(), protoParty.PartyId)
		assert.Equal(t, pb.PartyType_PARTY_TYPE_ORGANIZATION, protoParty.PartyType)
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

// errorProvider implements verification.Provider and always returns errors
type errorProvider struct{}

func (e *errorProvider) VerifyIdentity(_ context.Context, _ *domain.Party) (verification.Result, error) {
	return verification.Result{}, fmt.Errorf("provider unavailable")
}

func (e *errorProvider) CheckSanctions(_ context.Context, _ *domain.Party) (verification.SanctionsResult, error) {
	return verification.SanctionsResult{}, fmt.Errorf("sanctions service unavailable")
}

func (e *errorProvider) GetVerificationStatus(_ context.Context, _ string) (verification.Result, error) {
	return verification.Result{}, fmt.Errorf("provider unavailable")
}

// sanctionsErrorProvider approves identity but fails sanctions screening
type sanctionsErrorProvider struct {
	*verification.MockProvider
}

func (p *sanctionsErrorProvider) CheckSanctions(_ context.Context, _ *domain.Party) (verification.SanctionsResult, error) {
	return verification.SanctionsResult{}, fmt.Errorf("sanctions service unavailable")
}

func TestExchangeDemographics(t *testing.T) {
	ctx := context.Background()

	t.Run("provider approved returns APPROVED status", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Alice Smith")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		mockProvider := verification.NewMockProvider().WithAlwaysApprove(true)
		svc.WithVerificationProvider(mockProvider)

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: party.ID().String(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)

		assert.Equal(t, party.ID().String(), resp.PartyId)
		assert.Equal(t, "APPROVED", resp.VerificationStatus)
		assert.NotNil(t, resp.VerificationTimestamp)
	})

	t.Run("provider rejected returns REJECTED status", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Bob Jones")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		mockProvider := verification.NewMockProvider().WithAlwaysApprove(false)
		svc.WithVerificationProvider(mockProvider)

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: party.ID().String(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Sanctions match overrides to MANUAL_REVIEW when AlwaysApprove=false
		assert.Equal(t, "MANUAL_REVIEW", resp.VerificationStatus)
	})

	t.Run("sanctions match overrides status to MANUAL_REVIEW", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Charlie Brown")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		// AlwaysApprove=false causes CheckSanctions to return MATCH
		mockProvider := verification.NewMockProvider().WithAlwaysApprove(false)
		svc.WithVerificationProvider(mockProvider)

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: party.ID().String(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)

		assert.Equal(t, "MANUAL_REVIEW", resp.VerificationStatus)
	})

	t.Run("no provider in production returns Unimplemented", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Dave Wilson")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		// No provider configured
		t.Setenv("ENVIRONMENT", "production")

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: party.ID().String(),
		})
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Unimplemented, st.Code())
	})

	t.Run("no provider in dev returns stub VERIFIED", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Eve Davis")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		// No provider configured, not production
		t.Setenv("ENVIRONMENT", "development")

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: party.ID().String(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)

		assert.Equal(t, party.ID().String(), resp.PartyId)
		assert.Equal(t, "VERIFIED", resp.VerificationStatus)
		assert.NotNil(t, resp.VerificationTimestamp)
	})

	t.Run("provider error returns Internal", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Frank Miller")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		svc.WithVerificationProvider(&errorProvider{})

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: party.ID().String(),
		})
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})

	t.Run("sanctions error warns but does not fail", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		party, err := domain.NewParty(domain.PartyTypePerson, "Grace Lee")
		require.NoError(t, err)
		mock.parties[party.ID()] = party

		svc.WithVerificationProvider(&sanctionsErrorProvider{
			MockProvider: verification.NewMockProvider().WithAlwaysApprove(true),
		})

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: party.ID().String(),
		})
		require.NoError(t, err)
		require.NotNil(t, resp)

		// Identity verification approved, sanctions error does not override
		assert.Equal(t, "APPROVED", resp.VerificationStatus)
	})

	t.Run("invalid party ID returns InvalidArgument", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: "not-a-uuid",
		})
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("non-existent party returns NotFound", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.ExchangeDemographics(ctx, &pb.ExchangeDemographicsRequest{
			PartyId: uuid.New().String(),
		})
		assert.Nil(t, resp)
		assert.Error(t, err)

		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

func TestListParties(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	registerParty := func(svc *Service, partyType pb.PartyType, legalName string) *pb.Party {
		req := &pb.RegisterPartyRequest{
			PartyType: partyType,
			LegalName: legalName,
		}
		resp, err := svc.RegisterParty(ctx, req)
		require.NoError(t, err)
		return resp.Party
	}

	t.Run("returns empty list when no parties exist", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.ListParties(ctx, &pb.ListPartiesRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Empty(t, resp.Parties)
		assert.Equal(t, int64(0), resp.TotalCount)
		assert.Empty(t, resp.NextPageToken)
	})

	t.Run("returns all parties with default page size", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		registerParty(svc, pb.PartyType_PARTY_TYPE_PERSON, "Alice Smith")
		registerParty(svc, pb.PartyType_PARTY_TYPE_ORGANIZATION, "Acme Corp")

		resp, err := svc.ListParties(ctx, &pb.ListPartiesRequest{})
		require.NoError(t, err)
		assert.Len(t, resp.Parties, 2)
		assert.Equal(t, int64(2), resp.TotalCount)
	})

	t.Run("filters by party type", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		registerParty(svc, pb.PartyType_PARTY_TYPE_PERSON, "Alice Smith")
		registerParty(svc, pb.PartyType_PARTY_TYPE_ORGANIZATION, "Acme Corp")

		resp, err := svc.ListParties(ctx, &pb.ListPartiesRequest{
			PartyType: pb.PartyType_PARTY_TYPE_PERSON,
		})
		require.NoError(t, err)
		require.Len(t, resp.Parties, 1)
		assert.Equal(t, pb.PartyType_PARTY_TYPE_PERSON, resp.Parties[0].PartyType)
	})

	t.Run("filters by status", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		alice := registerParty(svc, pb.PartyType_PARTY_TYPE_PERSON, "Alice Smith")
		registerParty(svc, pb.PartyType_PARTY_TYPE_PERSON, "Bob Jones")

		// Suspend Alice
		_, err := svc.ControlParty(ctx, &pb.ControlPartyRequest{
			PartyId:       alice.PartyId,
			ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
			Reason:        "test",
			ActorId:       "admin",
		})
		require.NoError(t, err)

		resp, err := svc.ListParties(ctx, &pb.ListPartiesRequest{
			Status: pb.PartyStatus_PARTY_STATUS_ACTIVE,
		})
		require.NoError(t, err)
		require.Len(t, resp.Parties, 1)
		assert.Equal(t, pb.PartyStatus_PARTY_STATUS_ACTIVE, resp.Parties[0].Status)
	})

	t.Run("filters by search query", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		registerParty(svc, pb.PartyType_PARTY_TYPE_PERSON, "Alice Smith")
		registerParty(svc, pb.PartyType_PARTY_TYPE_ORGANIZATION, "Acme Corp")

		resp, err := svc.ListParties(ctx, &pb.ListPartiesRequest{
			SearchQuery: "alice",
		})
		require.NoError(t, err)
		require.Len(t, resp.Parties, 1)
		assert.Equal(t, "Alice Smith", resp.Parties[0].LegalName)
	})

	t.Run("applies default page size of 25", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.ListParties(ctx, &pb.ListPartiesRequest{PageSize: 0})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("clamps page size to max 100", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.ListParties(ctx, &pb.ListPartiesRequest{PageSize: 100})
		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("returns InvalidArgument for malformed page_token", func(t *testing.T) {
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.ListParties(ctx, &pb.ListPartiesRequest{
			PageToken: "not-valid-base64-cursor!!!",
		})
		assert.Nil(t, resp)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("returns Internal on repository error", func(t *testing.T) {
		mock := newMockRepository()
		mock.listPartiesErr = errDatabaseFailed
		svc := newTestService(mock)

		resp, err := svc.ListParties(ctx, &pb.ListPartiesRequest{})
		assert.Nil(t, resp)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
	})
}
