package service

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	quantityv1 "github.com/meridianhub/meridian/api/proto/meridian/quantity/v1"
	"github.com/meridianhub/meridian/services/party/domain"
	sharedcel "github.com/meridianhub/meridian/shared/pkg/cel"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

// newTestServiceWithValidator creates a Service with an AttributeValidator for testing.
func newTestServiceWithValidator(mock *mockRepository, ptRepo PartyTypeDefinitionRepository) *Service {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	svc, _ := NewService(mock, logger)

	compiler, err := sharedcel.NewCompiler()
	if err != nil {
		panic(err)
	}
	validator, err := NewAttributeValidator(ptRepo, compiler)
	if err != nil {
		panic(err)
	}
	svc.WithAttributeValidator(validator)
	return svc
}

// tenantCtx returns a context carrying the test tenant ID.
func tenantCtx() context.Context {
	return tenant.WithTenant(context.Background(), tenant.TenantID(testTenantID))
}

// TestRegisterParty_AttributeValidation tests attribute validation in RegisterParty.
func TestRegisterParty_AttributeValidation(t *testing.T) {
	t.Parallel()

	schema := `{
		"type": "object",
		"properties": {
			"annual_income": {"type": "string"}
		},
		"required": ["annual_income"]
	}`

	t.Run("valid attributes pass validation", func(t *testing.T) {
		mock := newMockRepository()
		ptRepo := newMockPartyTypeRepo()
		ptRepo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, "")

		svc := newTestServiceWithValidator(mock, ptRepo)

		resp, err := svc.RegisterParty(tenantCtx(), &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_PERSON,
			LegalName: "Alice Smith",
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "annual_income", Value: "50000"},
			},
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.NotEmpty(t, resp.Party.PartyId)
	})

	t.Run("missing required attribute fails validation", func(t *testing.T) {
		mock := newMockRepository()
		ptRepo := newMockPartyTypeRepo()
		ptRepo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, "")

		svc := newTestServiceWithValidator(mock, ptRepo)

		resp, err := svc.RegisterParty(tenantCtx(), &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_PERSON,
			LegalName: "Bob Jones",
			// No attributes - required annual_income missing
		})

		assert.Nil(t, resp)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
		assert.Contains(t, st.Message(), "attribute validation failed")
	})

	t.Run("no type definition skips validation", func(t *testing.T) {
		mock := newMockRepository()
		ptRepo := newMockPartyTypeRepo()
		// No definition registered for PERSON

		svc := newTestServiceWithValidator(mock, ptRepo)

		resp, err := svc.RegisterParty(tenantCtx(), &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_PERSON,
			LegalName: "Carol White",
			// No attributes - but no schema to validate against
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("no validator set skips validation", func(t *testing.T) {
		// Service without AttributeValidator - should work as before
		mock := newMockRepository()
		svc := newTestService(mock)

		resp, err := svc.RegisterParty(context.Background(), &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_PERSON,
			LegalName: "Dave Brown",
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("no tenant context skips validation", func(t *testing.T) {
		mock := newMockRepository()
		ptRepo := newMockPartyTypeRepo()
		ptRepo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, "")

		svc := newTestServiceWithValidator(mock, ptRepo)

		// No tenant context - validation skipped
		resp, err := svc.RegisterParty(context.Background(), &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_PERSON,
			LegalName: "Eve Davis",
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("CEL validation failure rejects registration", func(t *testing.T) {
		mock := newMockRepository()
		ptRepo := newMockPartyTypeRepo()
		celSchema := `{"type":"object","properties":{"income":{"type":"string"}}}`
		celExpr := `"income" in attributes && attributes["income"] != ""`
		ptRepo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", celSchema, celExpr)

		svc := newTestServiceWithValidator(mock, ptRepo)

		resp, err := svc.RegisterParty(tenantCtx(), &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_PERSON,
			LegalName: "Frank Lee",
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "income", Value: ""}, // empty income fails CEL
			},
		})

		assert.Nil(t, resp)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})
}

// TestUpdateParty_AttributeValidation tests attribute validation in UpdateParty.
func TestUpdateParty_AttributeValidation(t *testing.T) {
	t.Parallel()

	schema := `{
		"type": "object",
		"properties": {
			"annual_income": {"type": "string"}
		},
		"required": ["annual_income"]
	}`

	// Helper to set up a party in the mock repository
	setupParty := func(mock *mockRepository) *domain.Party {
		party, err := domain.NewParty(domain.PartyTypePerson, "Test Person")
		if err != nil {
			panic(err)
		}
		mock.parties[party.ID()] = party
		return party
	}

	t.Run("valid attributes pass validation with update mask", func(t *testing.T) {
		mock := newMockRepository()
		ptRepo := newMockPartyTypeRepo()
		ptRepo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, "")

		svc := newTestServiceWithValidator(mock, ptRepo)
		party := setupParty(mock)

		resp, err := svc.UpdateParty(tenantCtx(), &pb.UpdatePartyRequest{
			PartyId: party.ID().String(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"attributes"},
			},
			Attributes: []*quantityv1.AttributeEntry{
				{Key: "annual_income", Value: "60000"},
			},
			Version: int32(party.Version()),
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Len(t, resp.Party.Attributes, 1)
	})

	t.Run("invalid attributes fail validation with update mask", func(t *testing.T) {
		mock := newMockRepository()
		ptRepo := newMockPartyTypeRepo()
		ptRepo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, "")

		svc := newTestServiceWithValidator(mock, ptRepo)
		party := setupParty(mock)

		resp, err := svc.UpdateParty(tenantCtx(), &pb.UpdatePartyRequest{
			PartyId: party.ID().String(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"attributes"},
			},
			Attributes: []*quantityv1.AttributeEntry{
				// Missing required annual_income
				{Key: "other_field", Value: "value"},
			},
			Version: int32(party.Version()),
		})

		assert.Nil(t, resp)
		require.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("attributes not in update mask skips validation", func(t *testing.T) {
		mock := newMockRepository()
		ptRepo := newMockPartyTypeRepo()
		ptRepo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, "")

		svc := newTestServiceWithValidator(mock, ptRepo)
		party := setupParty(mock)

		// Update display_name only - attributes validation should be skipped
		resp, err := svc.UpdateParty(tenantCtx(), &pb.UpdatePartyRequest{
			PartyId: party.ID().String(),
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"display_name"},
			},
			DisplayName: "Updated Name",
			Version:     int32(party.Version()),
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})

	t.Run("no update mask applies attributes without required validation skipping", func(t *testing.T) {
		// Without field mask, the existing attributes on the party are not changed
		// unless explicitly updated (matching existing behavior)
		mock := newMockRepository()
		ptRepo := newMockPartyTypeRepo()
		ptRepo.entities[uuid.New()] = makePartyTypeDefinition(testTenantID, "PERSON", schema, "")

		svc := newTestServiceWithValidator(mock, ptRepo)
		party := setupParty(mock)

		// No field mask, no attributes provided - just update display name
		resp, err := svc.UpdateParty(tenantCtx(), &pb.UpdatePartyRequest{
			PartyId:     party.ID().String(),
			DisplayName: "New Name",
			Version:     int32(party.Version()),
		})

		require.NoError(t, err)
		require.NotNil(t, resp)
	})
}
