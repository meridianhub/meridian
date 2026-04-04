package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/lib/pq"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"gorm.io/gorm"
)

const testTenantID = "test_tenant"

// setupIntegrationTest creates a PostgreSQL testcontainer with the party schema
// and returns a configured Service for integration testing.
func setupIntegrationTest(t *testing.T) (*Service, *gorm.DB, context.Context, func()) {
	t.Helper()

	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.PartyEntity{},
		&audit.AuditOutbox{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the party table in the tenant schema (note: singular 'party' to match entity)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party (
		id UUID PRIMARY KEY,
		party_type VARCHAR(20) NOT NULL,
		legal_name VARCHAR(255) NOT NULL,
		display_name VARCHAR(255),
		status VARCHAR(20) NOT NULL,
		external_reference VARCHAR(255),
		external_reference_type VARCHAR(50),
		attributes JSONB NOT NULL DEFAULT '[]'::jsonb,
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		deleted_at TIMESTAMP WITH TIME ZONE,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		UNIQUE(external_reference, external_reference_type)
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the audit_outbox table in the tenant schema (required for audit hooks)
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.audit_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		table_name VARCHAR(100) NOT NULL,
		operation VARCHAR(10) NOT NULL,
		record_id VARCHAR(50) NOT NULL,
		old_values TEXT,
		new_values TEXT,
		status VARCHAR(20) NOT NULL DEFAULT 'pending',
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		retry_count INT NOT NULL DEFAULT 0,
		last_error TEXT,
		changed_by VARCHAR(100),
		transaction_id VARCHAR(100),
		client_ip VARCHAR(45),
		user_agent TEXT
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the party_association table in the tenant schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party_association (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		party_id UUID NOT NULL,
		related_party_id UUID NOT NULL,
		relationship_type VARCHAR(50) NOT NULL,
		metadata JSONB NULL DEFAULT '{}'::jsonb,
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		effective_from TIMESTAMPTZ NOT NULL DEFAULT now(),
		effective_to TIMESTAMPTZ NULL,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set default search_path to include tenant schema so Create/Update work in the tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc, err := NewService(repo, logger)
	require.NoError(t, err, "Failed to create service")

	return svc, db, ctx, cleanup
}

// TestRegisterParty_Person verifies successful registration of a person party
// with a Companies House reference number.
//
// Flow:
// 1. Register person with legal name and Companies House number
// 2. Verify response contains party_id and correct fields
// 3. Retrieve by ID and verify fields match
func TestRegisterParty_Person(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Register a person party
	registerReq := &pb.RegisterPartyRequest{
		PartyType:             pb.PartyType_PARTY_TYPE_PERSON,
		LegalName:             "John Smith",
		DisplayName:           "J. Smith",
		ExternalReference:     "12345678",
		ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
	}

	registerResp, err := svc.RegisterParty(ctx, registerReq)
	require.NoError(t, err, "RegisterParty should succeed")
	require.NotNil(t, registerResp.Party, "Response should contain party")

	// Verify registration response
	party := registerResp.Party
	assert.NotEmpty(t, party.PartyId, "Party ID should be generated")
	assert.Equal(t, pb.PartyType_PARTY_TYPE_PERSON, party.PartyType)
	assert.Equal(t, "John Smith", party.LegalName)
	assert.Equal(t, "J. Smith", party.DisplayName)
	assert.Equal(t, pb.PartyStatus_PARTY_STATUS_ACTIVE, party.Status, "Default status should be ACTIVE")
	assert.Equal(t, "12345678", party.ExternalReference)
	assert.Equal(t, pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE, party.ExternalReferenceType)
	assert.NotNil(t, party.CreatedAt, "CreatedAt should be set")
	assert.NotNil(t, party.UpdatedAt, "UpdatedAt should be set")
	// Version is 3 because domain model increments on: 1) NewParty, 2) SetDisplayName, 3) SetExternalReference
	assert.Equal(t, int32(3), party.Version, "Version should be 3 after setting display name and external reference")

	// Retrieve and verify persistence
	retrieveReq := &pb.RetrievePartyRequest{
		PartyId: party.PartyId,
	}

	retrieveResp, err := svc.RetrieveParty(ctx, retrieveReq)
	require.NoError(t, err, "RetrieveParty should succeed")

	retrievedParty := retrieveResp.Party
	assert.Equal(t, party.PartyId, retrievedParty.PartyId)
	assert.Equal(t, party.PartyType, retrievedParty.PartyType)
	assert.Equal(t, party.LegalName, retrievedParty.LegalName)
	assert.Equal(t, party.DisplayName, retrievedParty.DisplayName)
	assert.Equal(t, party.Status, retrievedParty.Status)
	assert.Equal(t, party.ExternalReference, retrievedParty.ExternalReference)
	assert.Equal(t, party.ExternalReferenceType, retrievedParty.ExternalReferenceType)
}

// TestRegisterParty_Organization verifies successful registration of an organization
// party with an LEI (Legal Entity Identifier).
//
// Flow:
// 1. Register organization with LEI
// 2. Verify status is ACTIVE by default
// 3. Retrieve by ID
func TestRegisterParty_Organization(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Register an organization party with LEI
	registerReq := &pb.RegisterPartyRequest{
		PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName:             "Acme Corporation Ltd",
		DisplayName:           "Acme Corp",
		ExternalReference:     "ABCD1234567890EFGH12", // 20 alphanumeric chars for LEI
		ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI,
	}

	registerResp, err := svc.RegisterParty(ctx, registerReq)
	require.NoError(t, err, "RegisterParty should succeed")
	require.NotNil(t, registerResp.Party)

	party := registerResp.Party
	assert.NotEmpty(t, party.PartyId)
	assert.Equal(t, pb.PartyType_PARTY_TYPE_ORGANIZATION, party.PartyType)
	assert.Equal(t, "Acme Corporation Ltd", party.LegalName)
	assert.Equal(t, pb.PartyStatus_PARTY_STATUS_ACTIVE, party.Status, "Default status should be ACTIVE")
	assert.Equal(t, "ABCD1234567890EFGH12", party.ExternalReference)
	assert.Equal(t, pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI, party.ExternalReferenceType)

	// Retrieve by ID to verify persistence
	retrieveResp, err := svc.RetrieveParty(ctx, &pb.RetrievePartyRequest{PartyId: party.PartyId})
	require.NoError(t, err)
	assert.Equal(t, party.PartyId, retrieveResp.Party.PartyId)
}

// TestRegisterParty_DuplicateExternalReference verifies that attempting to register
// a party with an external reference that already exists returns AlreadyExists error.
//
// Flow:
// 1. Register party with external reference
// 2. Attempt duplicate registration with same reference
// 3. Verify AlreadyExists error (gRPC code 6)
func TestRegisterParty_DuplicateExternalReference(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// First registration
	firstReq := &pb.RegisterPartyRequest{
		PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName:             "First Company Ltd",
		ExternalReference:     "AB123456", // Valid Companies House format
		ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
	}

	firstResp, err := svc.RegisterParty(ctx, firstReq)
	require.NoError(t, err, "First registration should succeed")
	require.NotNil(t, firstResp.Party)

	// Attempt duplicate registration with same external reference
	duplicateReq := &pb.RegisterPartyRequest{
		PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName:             "Second Company Ltd",
		ExternalReference:     "AB123456", // Same external reference
		ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
	}

	duplicateResp, err := svc.RegisterParty(ctx, duplicateReq)

	// Verify AlreadyExists error
	require.Error(t, err, "Duplicate registration should fail")
	assert.Nil(t, duplicateResp, "Response should be nil on error")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be a gRPC status error")
	assert.Equal(t, codes.AlreadyExists, st.Code(), "Error code should be AlreadyExists (6)")
	assert.Contains(t, st.Message(), "already exists", "Error message should mention already exists")
}

// TestRegisterParty_NoExternalReference verifies that parties can be registered
// without an external reference (external reference is optional).
func TestRegisterParty_NoExternalReference(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Register party without external reference
	registerReq := &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_PERSON,
		LegalName: "Jane Doe",
		// No external reference - should be allowed
	}

	registerResp, err := svc.RegisterParty(ctx, registerReq)
	require.NoError(t, err, "Registration without external reference should succeed")
	require.NotNil(t, registerResp.Party)

	party := registerResp.Party
	assert.NotEmpty(t, party.PartyId)
	assert.Equal(t, "Jane Doe", party.LegalName)
	assert.Empty(t, party.ExternalReference, "External reference should be empty")
	assert.Equal(t, pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_UNSPECIFIED, party.ExternalReferenceType)
}

// TestRetrieveParty_NotFound verifies that retrieving a non-existent party
// returns NotFound error (gRPC code 5).
func TestRetrieveParty_NotFound(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Generate a random UUID that doesn't exist
	nonExistentID := uuid.New().String()

	retrieveReq := &pb.RetrievePartyRequest{
		PartyId: nonExistentID,
	}

	resp, err := svc.RetrieveParty(ctx, retrieveReq)

	// Verify NotFound error
	require.Error(t, err, "Retrieve of non-existent party should fail")
	assert.Nil(t, resp, "Response should be nil on error")

	st, ok := status.FromError(err)
	require.True(t, ok, "Error should be a gRPC status error")
	assert.Equal(t, codes.NotFound, st.Code(), "Error code should be NotFound (5)")
	assert.Contains(t, st.Message(), "not found", "Error message should mention not found")
}

// TestRetrieveParty_InvalidUUID verifies that retrieving a party with a malformed
// party_id returns InvalidArgument error (gRPC code 3).
func TestRetrieveParty_InvalidUUID(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	testCases := []struct {
		name    string
		partyID string
	}{
		{"empty string", ""},
		{"not a uuid", "not-a-valid-uuid"},
		{"partial uuid", "550e8400-e29b-41d4"},
		{"uuid with extra chars", "550e8400-e29b-41d4-a716-446655440000xyz"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			retrieveReq := &pb.RetrievePartyRequest{
				PartyId: tc.partyID,
			}

			resp, err := svc.RetrieveParty(ctx, retrieveReq)

			// Verify InvalidArgument error
			require.Error(t, err, "Retrieve with invalid UUID should fail")
			assert.Nil(t, resp, "Response should be nil on error")

			st, ok := status.FromError(err)
			require.True(t, ok, "Error should be a gRPC status error")
			assert.Equal(t, codes.InvalidArgument, st.Code(), "Error code should be InvalidArgument (3)")
		})
	}
}

// TestRegisterParty_ValidationErrors verifies that invalid requests are rejected
// with InvalidArgument errors.
func TestRegisterParty_ValidationErrors(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	testCases := []struct {
		name        string
		req         *pb.RegisterPartyRequest
		errContains string
	}{
		{
			name: "empty legal name",
			req: &pb.RegisterPartyRequest{
				PartyType: pb.PartyType_PARTY_TYPE_PERSON,
				LegalName: "", // Empty - should fail
			},
			errContains: "legal name",
		},
		{
			name: "unspecified party type",
			req: &pb.RegisterPartyRequest{
				PartyType: pb.PartyType_PARTY_TYPE_UNSPECIFIED, // Invalid
				LegalName: "Test Party",
			},
			errContains: "party type",
		},
		{
			name: "external reference type without reference",
			req: &pb.RegisterPartyRequest{
				PartyType:             pb.PartyType_PARTY_TYPE_PERSON,
				LegalName:             "Test Party",
				ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI,
				// ExternalReference not set - should fail
			},
			errContains: "external reference",
		},
		{
			name: "invalid external reference format for LEI",
			req: &pb.RegisterPartyRequest{
				PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
				LegalName:             "Test Org",
				ExternalReference:     "invalid", // Not 20 alphanumeric chars
				ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI,
			},
			errContains: "external reference",
		},
		{
			name: "invalid external reference format for Companies House",
			req: &pb.RegisterPartyRequest{
				PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
				LegalName:             "Test Org",
				ExternalReference:     "invalid-format!", // Not valid Companies House format
				ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
			},
			errContains: "external reference",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := svc.RegisterParty(ctx, tc.req)

			require.Error(t, err, "Invalid request should fail")
			assert.Nil(t, resp, "Response should be nil on error")

			st, ok := status.FromError(err)
			require.True(t, ok, "Error should be a gRPC status error")
			assert.Equal(t, codes.InvalidArgument, st.Code(), "Error code should be InvalidArgument (3)")
			assert.Contains(t, st.Message(), tc.errContains, "Error message should contain expected text")
		})
	}
}

// TestRegisterParty_DifferentExternalRefTypes verifies that parties can have
// the same external reference value if the types are different.
func TestRegisterParty_DifferentExternalRefTypes(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Register party with Companies House reference
	firstReq := &pb.RegisterPartyRequest{
		PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName:             "Company One",
		ExternalReference:     "12345678",
		ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_COMPANIES_HOUSE,
	}

	firstResp, err := svc.RegisterParty(ctx, firstReq)
	require.NoError(t, err, "First registration should succeed")
	require.NotNil(t, firstResp.Party)

	// Register party with National ID - same reference value but different type
	// This should succeed because uniqueness is per reference type
	secondReq := &pb.RegisterPartyRequest{
		PartyType:             pb.PartyType_PARTY_TYPE_PERSON,
		LegalName:             "Person One",
		ExternalReference:     "12345678", // Same value
		ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_NATIONAL_ID,
	}

	secondResp, err := svc.RegisterParty(ctx, secondReq)
	require.NoError(t, err, "Registration with same reference but different type should succeed")
	require.NotNil(t, secondResp.Party)

	// Verify both parties exist and have different IDs
	assert.NotEqual(t, firstResp.Party.PartyId, secondResp.Party.PartyId)
}

// TestRegisterAndRetrieve_EndToEnd performs a complete end-to-end flow:
// Register → Retrieve → Verify all fields
func TestRegisterAndRetrieve_EndToEnd(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Register a comprehensive party (LEI must be 20 uppercase alphanumeric chars)
	registerReq := &pb.RegisterPartyRequest{
		PartyType:             pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName:             "Meridian Financial Services Limited",
		DisplayName:           "Meridian FS",
		ExternalReference:     "MERI123456789012345A", // 20 char uppercase alphanumeric LEI
		ExternalReferenceType: pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI,
	}

	registerResp, err := svc.RegisterParty(ctx, registerReq)
	require.NoError(t, err)

	registeredParty := registerResp.Party
	partyID := registeredParty.PartyId

	// Retrieve and verify complete data integrity
	retrieveResp, err := svc.RetrieveParty(ctx, &pb.RetrievePartyRequest{PartyId: partyID})
	require.NoError(t, err)

	retrieved := retrieveResp.Party
	assert.Equal(t, partyID, retrieved.PartyId)
	assert.Equal(t, pb.PartyType_PARTY_TYPE_ORGANIZATION, retrieved.PartyType)
	assert.Equal(t, "Meridian Financial Services Limited", retrieved.LegalName)
	assert.Equal(t, "Meridian FS", retrieved.DisplayName)
	assert.Equal(t, pb.PartyStatus_PARTY_STATUS_ACTIVE, retrieved.Status)
	assert.Equal(t, "MERI123456789012345A", retrieved.ExternalReference)
	assert.Equal(t, pb.ExternalReferenceType_EXTERNAL_REFERENCE_TYPE_LEI, retrieved.ExternalReferenceType)
	// Version is 3: 1) NewParty, 2) SetDisplayName, 3) SetExternalReference
	assert.Equal(t, int32(3), retrieved.Version)
	assert.NotNil(t, retrieved.CreatedAt)
	assert.NotNil(t, retrieved.UpdatedAt)
}

// TestNewService_NilRepository verifies that creating a service with nil repository
// returns an error.
func TestNewService_NilRepository(t *testing.T) {
	svc, err := NewService(nil, nil)

	require.Error(t, err, "Creating service with nil repository should fail")
	assert.Nil(t, svc, "Service should be nil")
	assert.ErrorIs(t, err, ErrRepositoryNil)
}

// createSyndicateWithParticipants creates an org party and N participant parties
// with SYNDICATE_PARTICIPANT associations, returning the org party ID and participant IDs.
func createSyndicateWithParticipants(t *testing.T, svc *Service, ctx context.Context, numParticipants int) (string, []string) {
	t.Helper()

	// Create org party (syndicate host)
	orgResp, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName: "Syndicate Host Ltd",
	})
	require.NoError(t, err)
	orgPartyID := orgResp.Party.PartyId

	participantIDs := make([]string, numParticipants)
	for i := 0; i < numParticipants; i++ {
		// Create participant party
		partResp, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
			PartyType: pb.PartyType_PARTY_TYPE_ORGANIZATION,
			LegalName: fmt.Sprintf("Participant %d Ltd", i+1),
		})
		require.NoError(t, err)
		participantIDs[i] = partResp.Party.PartyId

		// Create syndicate participant association with metadata
		metadata, err := structpb.NewStruct(map[string]interface{}{
			"allocation_share": float64(100) / float64(numParticipants),
			"role":             fmt.Sprintf("participant_%d", i+1),
		})
		require.NoError(t, err)

		_, err = svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
			PartyId:          participantIDs[i],
			RelatedPartyId:   orgPartyID,
			RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
			Metadata:         metadata,
			Status:           pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE,
		})
		require.NoError(t, err)
	}

	return orgPartyID, participantIDs
}

// TestListParticipants_ReturnsActiveParticipants verifies that ListParticipants
// returns all active syndicate participants for an organization party.
func TestListParticipants_ReturnsActiveParticipants(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	orgPartyID, participantIDs := createSyndicateWithParticipants(t, svc, ctx, 3)

	resp, err := svc.ListParticipants(ctx, &pb.ListParticipantsRequest{
		OrgPartyId:       orgPartyID,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
	})
	require.NoError(t, err)
	require.Len(t, resp.Participants, 3, "Should return 3 active participants")

	// Verify each participant has the expected fields
	returnedPartyIDs := make(map[string]bool)
	for _, p := range resp.Participants {
		assert.NotEmpty(t, p.AssociationId)
		assert.Equal(t, pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT, p.RelationshipType)
		assert.Equal(t, pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE, p.Status)
		assert.NotNil(t, p.CreatedAt)
		assert.NotNil(t, p.EffectiveFrom)
		assert.NotNil(t, p.Metadata)
		// The related_party_id in the association is the org party
		assert.Equal(t, orgPartyID, p.RelatedPartyId)
		returnedPartyIDs[p.AssociationId] = true
	}

	// Verify all 3 distinct association IDs returned
	assert.Len(t, returnedPartyIDs, 3)

	_ = participantIDs // used by createSyndicateWithParticipants
}

// TestListParticipants_ExcludesSuspended verifies that SUSPENDED associations
// are excluded from ListParticipants results.
func TestListParticipants_ExcludesSuspended(t *testing.T) {
	svc, db, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	orgPartyID, _ := createSyndicateWithParticipants(t, svc, ctx, 3)

	// Suspend one participant's association directly in DB
	err := db.Exec("UPDATE party_association SET status = 'SUSPENDED' WHERE related_party_id = ? LIMIT 1",
		orgPartyID).Error
	// LIMIT not supported in all DB engines, use a subquery approach instead
	if err != nil {
		// Fallback: update first association found
		var assocID string
		db.Raw("SELECT id FROM party_association WHERE related_party_id = ? LIMIT 1", orgPartyID).Scan(&assocID)
		err = db.Exec("UPDATE party_association SET status = 'SUSPENDED' WHERE id = ?", assocID).Error
		require.NoError(t, err)
	}

	resp, err := svc.ListParticipants(ctx, &pb.ListParticipantsRequest{
		OrgPartyId:       orgPartyID,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
	})
	require.NoError(t, err)
	assert.Len(t, resp.Participants, 2, "Should return only 2 active participants (1 suspended)")

	for _, p := range resp.Participants {
		assert.Equal(t, pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE, p.Status)
	}
}

// TestListParticipants_EmptyResult verifies that ListParticipants returns
// an empty list (not an error) when no participants exist.
func TestListParticipants_EmptyResult(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Create an org party with no participants
	orgResp, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName: "Empty Syndicate Ltd",
	})
	require.NoError(t, err)

	resp, err := svc.ListParticipants(ctx, &pb.ListParticipantsRequest{
		OrgPartyId:       orgResp.Party.PartyId,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
	})
	require.NoError(t, err)
	assert.Empty(t, resp.Participants)
}

// TestListParticipants_InvalidOrgPartyID verifies that an invalid UUID returns InvalidArgument.
func TestListParticipants_InvalidOrgPartyID(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	resp, err := svc.ListParticipants(ctx, &pb.ListParticipantsRequest{
		OrgPartyId:       "not-a-uuid",
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
	})
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestGetStructuringData_ReturnsMetadata verifies that GetStructuringData
// returns the correct metadata for a specific participant.
func TestGetStructuringData_ReturnsMetadata(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	orgPartyID, participantIDs := createSyndicateWithParticipants(t, svc, ctx, 2)

	// Get structuring data for first participant
	resp, err := svc.GetStructuringData(ctx, &pb.GetStructuringDataRequest{
		PartyId:          participantIDs[0],
		OrgPartyId:       orgPartyID,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Metadata)

	metadataMap := resp.Metadata.AsMap()
	assert.Equal(t, float64(50), metadataMap["allocation_share"])
	assert.Equal(t, "participant_1", metadataMap["role"])
}

// TestGetStructuringData_NotFoundReturnsEmpty verifies that GetStructuringData
// returns an empty metadata map (not an error) when no association exists.
func TestGetStructuringData_NotFoundReturnsEmpty(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	resp, err := svc.GetStructuringData(ctx, &pb.GetStructuringDataRequest{
		PartyId:          uuid.New().String(),
		OrgPartyId:       uuid.New().String(),
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
	})
	require.NoError(t, err)
	require.NotNil(t, resp.Metadata)

	// Should be empty, not nil
	metadataMap := resp.Metadata.AsMap()
	assert.Empty(t, metadataMap)
}

// TestGetStructuringData_InvalidPartyID verifies that invalid UUIDs return InvalidArgument.
func TestGetStructuringData_InvalidPartyID(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	resp, err := svc.GetStructuringData(ctx, &pb.GetStructuringDataRequest{
		PartyId:          "not-a-uuid",
		OrgPartyId:       uuid.New().String(),
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
	})
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestGetStructuringData_InvalidOrgPartyID verifies that invalid org party UUID returns InvalidArgument.
func TestGetStructuringData_InvalidOrgPartyID(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	resp, err := svc.GetStructuringData(ctx, &pb.GetStructuringDataRequest{
		PartyId:          uuid.New().String(),
		OrgPartyId:       "not-a-uuid",
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
	})
	assert.Nil(t, resp)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

// TestRegisterAssociations_WithMetadata verifies that associations can be
// created with metadata and the new lifecycle fields.
func TestRegisterAssociations_WithMetadata(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Create two parties
	party1Resp, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName: "Host Corp",
	})
	require.NoError(t, err)

	party2Resp, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName: "Participant Corp",
	})
	require.NoError(t, err)

	metadata, err := structpb.NewStruct(map[string]interface{}{
		"allocation_share": 25.5,
		"tier":             "gold",
	})
	require.NoError(t, err)

	assocResp, err := svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
		PartyId:          party2Resp.Party.PartyId,
		RelatedPartyId:   party1Resp.Party.PartyId,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
		Metadata:         metadata,
		Status:           pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE,
	})
	require.NoError(t, err)
	assert.NotEmpty(t, assocResp.AssociationId)
	assert.Equal(t, pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE, assocResp.Status)

	// Verify metadata was persisted via GetStructuringData
	structResp, err := svc.GetStructuringData(ctx, &pb.GetStructuringDataRequest{
		PartyId:          party2Resp.Party.PartyId,
		OrgPartyId:       party1Resp.Party.PartyId,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
	})
	require.NoError(t, err)

	metadataMap := structResp.Metadata.AsMap()
	assert.Equal(t, 25.5, metadataMap["allocation_share"])
	assert.Equal(t, "gold", metadataMap["tier"])
}

// TestRetrieveAssociations_IncludesNewFields verifies that RetrieveAssociations
// returns the new metadata, status, effective_from, effective_to fields.
func TestRetrieveAssociations_IncludesNewFields(t *testing.T) {
	svc, _, ctx, cleanup := setupIntegrationTest(t)
	defer cleanup()

	// Create two parties and an association with metadata
	party1Resp, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName: "Host Corp",
	})
	require.NoError(t, err)

	party2Resp, err := svc.RegisterParty(ctx, &pb.RegisterPartyRequest{
		PartyType: pb.PartyType_PARTY_TYPE_ORGANIZATION,
		LegalName: "Partner Corp",
	})
	require.NoError(t, err)

	metadata, err := structpb.NewStruct(map[string]interface{}{
		"allocation_share": 50.0,
	})
	require.NoError(t, err)

	_, err = svc.RegisterAssociations(ctx, &pb.RegisterAssociationsRequest{
		PartyId:          party2Resp.Party.PartyId,
		RelatedPartyId:   party1Resp.Party.PartyId,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SYNDICATE_PARTICIPANT,
		Metadata:         metadata,
	})
	require.NoError(t, err)

	// Retrieve associations for party2
	assocResp, err := svc.RetrieveAssociations(ctx, &pb.RetrieveAssociationsRequest{
		PartyId: party2Resp.Party.PartyId,
	})
	require.NoError(t, err)
	require.Len(t, assocResp.Associations, 1)

	assoc := assocResp.Associations[0]
	assert.Equal(t, pb.AssociationStatus_ASSOCIATION_STATUS_ACTIVE, assoc.Status)
	assert.NotNil(t, assoc.EffectiveFrom)
	assert.Nil(t, assoc.EffectiveTo)
	assert.NotNil(t, assoc.Metadata)

	metadataMap := assoc.Metadata.AsMap()
	assert.Equal(t, 50.0, metadataMap["allocation_share"])
}

// Suppress unused import warnings
var (
	_ = json.Marshal
	_ = structpb.NewStruct
)
