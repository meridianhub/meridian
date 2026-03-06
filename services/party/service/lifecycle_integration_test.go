// Package service provides integration tests for Party Service lifecycle operations,
// including CR operations (UpdateParty, ControlParty) and BQ operations (Reference,
// Associations, Demographics, BankRelations), with tests for cascade effects,
// end-to-end workflows, concurrency, and event ordering.
package service

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"

	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/audit"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// setupLifecycleIntegrationTest creates a PostgreSQL testcontainer with full schema
// including party, reference, associations, demographics, bank_relations, and audit_outbox.
func setupLifecycleIntegrationTest(t *testing.T) (*Service, *gorm.DB, context.Context, func()) {
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

	// Create the party table
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

	// Create the party_reference table (BQ: Reference) - matches migration schema
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party_reference (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		party_id UUID NOT NULL REFERENCES %[1]s.party(id) ON DELETE CASCADE,
		reference_type VARCHAR(50) NOT NULL,
		reference_value VARCHAR(255) NOT NULL,
		issuing_authority VARCHAR(100),
		issue_date DATE,
		expiry_date DATE,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create indexes matching migration
	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_party_reference_party_id
		ON %s.party_reference(party_id)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_party_reference_type_value
		ON %s.party_reference(reference_type, reference_value)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_party_reference_expiry_date
		ON %s.party_reference(expiry_date) WHERE expiry_date IS NOT NULL`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the party_association table (BQ: Associations) - singular to match migration
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party_association (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		party_id UUID NOT NULL REFERENCES %[1]s.party(id) ON DELETE CASCADE,
		related_party_id UUID NOT NULL,
		relationship_type VARCHAR(50) NOT NULL,
		metadata JSONB NULL DEFAULT '{}'::jsonb,
		status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE',
		effective_from TIMESTAMPTZ NOT NULL DEFAULT now(),
		effective_to TIMESTAMPTZ NULL,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		UNIQUE(party_id, related_party_id, relationship_type)
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the party_demographic table (BQ: Demographics) - singular to match migration
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party_demographic (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		party_id UUID NOT NULL REFERENCES %[1]s.party(id) ON DELETE CASCADE,
		socio_economic_data JSONB,
		employment_history JSONB,
		income_level VARCHAR(50),
		education_level VARCHAR(50),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		CONSTRAINT uq_party_demographic_party_id UNIQUE(party_id)
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create indexes matching migration
	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_party_demographic_party_id
		ON %s.party_demographic(party_id)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_party_demographic_socio_economic
		ON %s.party_demographic USING GIN(socio_economic_data)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_party_demographic_employment
		ON %s.party_demographic USING GIN(employment_history)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the party_bank_relation table (BQ: BankRelations) - singular to match migration
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party_bank_relation (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		party_id UUID NOT NULL REFERENCES %[1]s.party(id) ON DELETE CASCADE,
		account_officer_id VARCHAR(100),
		relationship_manager_id VARCHAR(100),
		assigned_branch VARCHAR(100),
		relationship_start_date DATE,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		CONSTRAINT uq_party_bank_relation_party_id UNIQUE(party_id)
	)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create indexes matching migration
	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_party_bank_relation_party_id
		ON %s.party_bank_relation(party_id)`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_party_bank_relation_account_officer
		ON %s.party_bank_relation(account_officer_id) WHERE account_officer_id IS NOT NULL`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	err = db.Exec(fmt.Sprintf(`CREATE INDEX IF NOT EXISTS idx_party_bank_relation_relationship_manager
		ON %s.party_bank_relation(relationship_manager_id) WHERE relationship_manager_id IS NOT NULL`, pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the audit_outbox table for event publishing
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.audit_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		table_name VARCHAR(100) NOT NULL,
		operation VARCHAR(10) NOT NULL,
		record_id UUID NOT NULL,
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

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc, err := NewService(repo, logger)
	require.NoError(t, err, "Failed to create service")

	return svc, db, ctx, cleanup
}

// registerTestParty is a helper to create a party for testing lifecycle operations
func registerTestParty(t *testing.T, ctx context.Context, svc *Service, legalName string) *pb.Party {
	t.Helper()

	req := &pb.RegisterPartyRequest{
		PartyType:   pb.PartyType_PARTY_TYPE_PERSON,
		LegalName:   legalName,
		DisplayName: legalName,
	}

	resp, err := svc.RegisterParty(ctx, req)
	require.NoError(t, err, "Failed to register test party")
	require.NotNil(t, resp.Party)

	return resp.Party
}

// --- End-to-End BQ Workflow Tests ---

// TestEndToEnd_CompletePartyLifecycle verifies a complete party lifecycle:
// Register → UpdateReference → RegisterAssociations → UpdateDemographics →
// UpdateBankRelations → RetrieveParty (verify all BQ data returned)
func TestEndToEnd_CompletePartyLifecycle(t *testing.T) {
	svc, _, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	// Step 1: Register party
	party := registerTestParty(t, ctx, svc, "John Doe")

	// Step 2: Update Reference (government ID)
	refReq := &pb.UpdateReferenceRequest{
		PartyId:          party.PartyId,
		GovernmentId:     "PASSPORT-123456",
		TaxReference:     "TAX-789",
		IssuingAuthority: "HM Passport Office",
		ExpiryDate:       "2030-12-31",
	}
	refResp, err := svc.UpdateReference(ctx, refReq)
	require.NoError(t, err, "UpdateReference should succeed")
	assert.Equal(t, party.PartyId, refResp.PartyId)
	assert.Equal(t, "PASSPORT-123456", refResp.GovernmentId)

	// Step 3: Register Associations (add spouse)
	spouse := registerTestParty(t, ctx, svc, "Jane Doe")
	assocReq := &pb.RegisterAssociationsRequest{
		PartyId:          party.PartyId,
		RelatedPartyId:   spouse.PartyId,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_BENEFICIAL_OWNER,
	}
	assocResp, err := svc.RegisterAssociations(ctx, assocReq)
	require.NoError(t, err, "RegisterAssociations should succeed")
	assert.NotEmpty(t, assocResp.AssociationId)

	// Step 4: Update Demographics (add employment)
	demoReq := &pb.UpdateDemographicsRequest{
		PartyId:           party.PartyId,
		SocioEconomicData: "Middle income bracket",
		EmploymentHistory: "Software Engineer at Acme Corp (2020-present)",
	}
	demoResp, err := svc.UpdateDemographics(ctx, demoReq)
	require.NoError(t, err, "UpdateDemographics should succeed")
	assert.Equal(t, party.PartyId, demoResp.PartyId)

	// Step 5: Update Bank Relations (assign account officer)
	bankReq := &pb.UpdateBankRelationsRequest{
		PartyId:               party.PartyId,
		AccountOfficerId:      "OFFICER-001",
		RelationshipManagerId: "MGR-002",
		AssignedBranch:        "London-Canary-Wharf",
	}
	bankResp, err := svc.UpdateBankRelations(ctx, bankReq)
	require.NoError(t, err, "UpdateBankRelations should succeed")
	assert.Equal(t, "OFFICER-001", bankResp.AccountOfficerId)

	// Step 6: Retrieve party and verify all data is accessible
	retrieveResp, err := svc.RetrieveParty(ctx, &pb.RetrievePartyRequest{PartyId: party.PartyId})
	require.NoError(t, err, "RetrieveParty should succeed")
	assert.Equal(t, party.PartyId, retrieveResp.Party.PartyId)
	assert.Equal(t, "John Doe", retrieveResp.Party.LegalName)

	// Verify Reference data is retrievable
	retrieveRefResp, err := svc.RetrieveReference(ctx, &pb.RetrieveReferenceRequest{PartyId: party.PartyId})
	require.NoError(t, err, "RetrieveReference should succeed")
	assert.Equal(t, "PASSPORT-123456", retrieveRefResp.GovernmentId)
	assert.Equal(t, "TAX-789", retrieveRefResp.TaxReference)

	// Verify Associations data is retrievable
	retrieveAssocResp, err := svc.RetrieveAssociations(ctx, &pb.RetrieveAssociationsRequest{PartyId: party.PartyId})
	require.NoError(t, err, "RetrieveAssociations should succeed")
	assert.Len(t, retrieveAssocResp.Associations, 1)
	assert.Equal(t, spouse.PartyId, retrieveAssocResp.Associations[0].RelatedPartyId)

	// Verify Demographics data is retrievable
	retrieveDemoResp, err := svc.RetrieveDemographics(ctx, &pb.RetrieveDemographicsRequest{PartyId: party.PartyId})
	require.NoError(t, err, "RetrieveDemographics should succeed")
	assert.Equal(t, "Middle income bracket", retrieveDemoResp.SocioEconomicData)

	// Verify BankRelations data is retrievable
	retrieveBankResp, err := svc.RetrieveBankRelations(ctx, &pb.RetrieveBankRelationsRequest{PartyId: party.PartyId})
	require.NoError(t, err, "RetrieveBankRelations should succeed")
	assert.Equal(t, "OFFICER-001", retrieveBankResp.AccountOfficerId)
}

// TestEndToEnd_ExpiredReferenceUpdate verifies handling of expired reference documents:
// Party with expired government_id → UpdateReference with new document →
// verify old reference archived, new reference active
func TestEndToEnd_ExpiredReferenceUpdate(t *testing.T) {
	t.Skip("Requires is_active column for reference archival - future enhancement")
	svc, db, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	// Register party
	party := registerTestParty(t, ctx, svc, "Alice Smith")

	// Add initial reference with expired date (in the past)
	expiredReq := &pb.UpdateReferenceRequest{
		PartyId:          party.PartyId,
		GovernmentId:     "OLD-PASSPORT-999",
		TaxReference:     "TAX-OLD",
		IssuingAuthority: "Old Authority",
		ExpiryDate:       "2020-01-01", // Expired
	}
	_, err := svc.UpdateReference(ctx, expiredReq)
	require.NoError(t, err)

	// Update with new reference document
	newReq := &pb.UpdateReferenceRequest{
		PartyId:          party.PartyId,
		GovernmentId:     "NEW-PASSPORT-123",
		TaxReference:     "TAX-NEW",
		IssuingAuthority: "HM Passport Office",
		ExpiryDate:       "2035-12-31", // Future date
	}
	newResp, err := svc.UpdateReference(ctx, newReq)
	require.NoError(t, err)
	assert.Equal(t, "NEW-PASSPORT-123", newResp.GovernmentId)

	// Verify old reference is archived (is_active = false)
	var oldRefActive bool
	err = db.Raw(fmt.Sprintf(`
		SELECT is_active FROM %s.party_reference
		WHERE party_id = ? AND government_id = 'OLD-PASSPORT-999'
	`, pq.QuoteIdentifier(tenant.TenantID(testTenantID).SchemaName())), party.PartyId).Scan(&oldRefActive).Error
	require.NoError(t, err)
	assert.False(t, oldRefActive, "Old reference should be archived (is_active=false)")

	// Verify new reference is active
	retrieveResp, err := svc.RetrieveReference(ctx, &pb.RetrieveReferenceRequest{PartyId: party.PartyId})
	require.NoError(t, err)
	assert.Equal(t, "NEW-PASSPORT-123", retrieveResp.GovernmentId)
	assert.Equal(t, "2035-12-31", retrieveResp.ExpiryDate)
}

// TestEndToEnd_BankRelationsWithControlParty verifies that:
// UpdateBankRelations (assign account officer) → ControlParty(RESTRICT) →
// verify bank officer can be notified (via audit_outbox event check)
func TestEndToEnd_BankRelationsWithControlParty(t *testing.T) {
	t.Skip("Requires ControlParty audit event publishing - future enhancement")
	svc, db, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	// Register party and assign bank officer
	party := registerTestParty(t, ctx, svc, "Bob Williams")

	bankReq := &pb.UpdateBankRelationsRequest{
		PartyId:          party.PartyId,
		AccountOfficerId: "OFFICER-123",
		AssignedBranch:   "Manchester-City-Center",
	}
	_, err := svc.UpdateBankRelations(ctx, bankReq)
	require.NoError(t, err)

	// Control party: RESTRICT
	controlReq := &pb.ControlPartyRequest{
		PartyId:       party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_RESTRICT,
		Reason:        "Pending KYC review",
		ActorId:       "COMPLIANCE-OFFICER-001",
	}
	controlResp, err := svc.ControlParty(ctx, controlReq)
	require.NoError(t, err)
	assert.Equal(t, pb.PartyStatus_PARTY_STATUS_RESTRICTED, controlResp.Party.Status)

	// Verify event published to audit_outbox (for future Kafka publishing)
	var outboxCount int64
	err = db.Raw(fmt.Sprintf(`
		SELECT COUNT(*) FROM %s.audit_outbox
		WHERE table_name = 'party' AND record_id = ? AND operation = 'UPDATE'
	`, pq.QuoteIdentifier(tenant.TenantID(testTenantID).SchemaName())), party.PartyId).Scan(&outboxCount).Error
	require.NoError(t, err)
	assert.Greater(t, outboxCount, int64(0), "Audit outbox should contain event for party status change")
}

// --- Concurrency and Race Condition Tests ---

// TestConcurrency_UpdatePartySameParty verifies optimistic locking:
// Concurrent UpdateParty calls on same party → verify only one succeeds, others get version conflict
func TestConcurrency_UpdatePartySameParty(t *testing.T) {
	t.Skip("Requires full optimistic locking implementation - future enhancement")
	svc, _, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	// Register party
	party := registerTestParty(t, ctx, svc, "Concurrent Test Party")

	// Concurrent update attempts
	const numConcurrent = 5
	results := make(chan error, numConcurrent)
	var wg sync.WaitGroup

	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			updateReq := &pb.UpdatePartyRequest{
				PartyId:   party.PartyId,
				LegalName: fmt.Sprintf("Updated Name %d", idx),
				Version:   party.Version, // All using same version - optimistic lock test
			}
			_, err := svc.UpdateParty(ctx, updateReq)
			results <- err
		}(i)
	}

	wg.Wait()
	close(results)

	// Count successes and version conflicts
	var successCount, versionConflictCount int
	for err := range results {
		if err == nil {
			successCount++
		} else {
			st, ok := status.FromError(err)
			if ok && st.Code() == codes.Aborted {
				versionConflictCount++
			}
		}
	}

	// Only ONE should succeed due to optimistic locking
	assert.Equal(t, 1, successCount, "Only one concurrent update should succeed")
	assert.Equal(t, numConcurrent-1, versionConflictCount, "Others should fail with version conflict")
}

// TestConcurrency_RegisterAssociationsSameRelatedParty verifies UNIQUE constraint:
// Concurrent RegisterAssociations with same related_party_id → verify UNIQUE constraint enforced
func TestConcurrency_RegisterAssociationsSameRelatedParty(t *testing.T) {
	t.Skip("Requires RegisterAssociations with UNIQUE constraint handling - future enhancement")
	svc, _, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	// Register two parties
	party := registerTestParty(t, ctx, svc, "Primary Party")
	relatedParty := registerTestParty(t, ctx, svc, "Related Party")

	// Concurrent association registration attempts with same related party and relationship type
	const numConcurrent = 3
	results := make(chan error, numConcurrent)
	var wg sync.WaitGroup

	for i := 0; i < numConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			assocReq := &pb.RegisterAssociationsRequest{
				PartyId:          party.PartyId,
				RelatedPartyId:   relatedParty.PartyId,
				RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
			}
			_, err := svc.RegisterAssociations(ctx, assocReq)
			results <- err
		}()
	}

	wg.Wait()
	close(results)

	// Count successes and unique constraint violations
	var successCount, alreadyExistsCount int
	for err := range results {
		if err == nil {
			successCount++
		} else {
			st, ok := status.FromError(err)
			if ok && st.Code() == codes.AlreadyExists {
				alreadyExistsCount++
			}
		}
	}

	// Only ONE should succeed due to UNIQUE constraint
	assert.Equal(t, 1, successCount, "Only one association should succeed")
	assert.Equal(t, numConcurrent-1, alreadyExistsCount, "Others should fail with AlreadyExists")
}

// --- Event Ordering Validation Tests ---

// TestEventOrdering_UpdateThenControl verifies event ordering:
// UpdateParty → ControlParty(TERMINATE) → verify events published in correct order
// and timestamps are monotonically increasing
func TestEventOrdering_UpdateThenControl(t *testing.T) {
	t.Skip("Requires UpdateParty audit event publishing - future enhancement")
	svc, db, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	// Register party
	party := registerTestParty(t, ctx, svc, "Event Order Test Party")

	// Step 1: Update party
	updateReq := &pb.UpdatePartyRequest{
		PartyId:     party.PartyId,
		DisplayName: "Updated Display Name",
		Version:     party.Version,
	}
	updateResp, err := svc.UpdateParty(ctx, updateReq)
	require.NoError(t, err)

	// Step 2: Control party (TERMINATE)
	controlReq := &pb.ControlPartyRequest{
		PartyId:       party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_TERMINATE,
		Reason:        "Customer request",
		ActorId:       "ADMIN-001",
	}
	_, err = svc.ControlParty(ctx, controlReq)
	require.NoError(t, err)

	// Verify events in audit_outbox are in correct order
	type outboxEvent struct {
		ID        string
		CreatedAt time.Time
		Operation string
	}
	var events []outboxEvent
	err = db.Raw(fmt.Sprintf(`
		SELECT id, created_at, operation FROM %s.audit_outbox
		WHERE table_name = 'party' AND record_id = ?
		ORDER BY created_at ASC
	`, pq.QuoteIdentifier(tenant.TenantID(testTenantID).SchemaName())), party.PartyId).Scan(&events).Error
	require.NoError(t, err)

	// Should have at least 2 events (initial register + update + control)
	assert.GreaterOrEqual(t, len(events), 2, "Should have multiple events in audit_outbox")

	// Verify timestamps are monotonically increasing
	for i := 1; i < len(events); i++ {
		assert.True(t, events[i].CreatedAt.After(events[i-1].CreatedAt) || events[i].CreatedAt.Equal(events[i-1].CreatedAt),
			"Event timestamps should be monotonically increasing")
	}

	// Verify the update happened before the control (by checking party version)
	assert.Greater(t, updateResp.Party.Version, party.Version, "Update should increment version")
}

// Cross-Service Cascade Tests have been moved to kafka_cascade_integration_test.go
// with real Kafka testcontainer infrastructure. See:
// - TestCascade_PartyTerminatedPublishesEvent
// - TestCascade_EventReplayIdempotency
// - TestCascade_PartialFailureRecovery
// - TestCascade_LoadTest100Terminations
// - TestCascade_NegativeControlNonExistentParty

// --- Additional Workflow Tests ---

// TestWorkflow_MultipleAssociationsForParty verifies that a party can have
// multiple associations with different parties and relationship types.
func TestWorkflow_MultipleAssociationsForParty(t *testing.T) {
	svc, _, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	// Register primary party and related parties
	primaryParty := registerTestParty(t, ctx, svc, "Primary Business")
	partner1 := registerTestParty(t, ctx, svc, "Business Partner 1")
	partner2 := registerTestParty(t, ctx, svc, "Business Partner 2")
	guarantor := registerTestParty(t, ctx, svc, "Guarantor Corp")

	// Register multiple associations
	assoc1Req := &pb.RegisterAssociationsRequest{
		PartyId:          primaryParty.PartyId,
		RelatedPartyId:   partner1.PartyId,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER,
	}
	_, err := svc.RegisterAssociations(ctx, assoc1Req)
	require.NoError(t, err)

	assoc2Req := &pb.RegisterAssociationsRequest{
		PartyId:          primaryParty.PartyId,
		RelatedPartyId:   partner2.PartyId,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER,
	}
	_, err = svc.RegisterAssociations(ctx, assoc2Req)
	require.NoError(t, err)

	assoc3Req := &pb.RegisterAssociationsRequest{
		PartyId:          primaryParty.PartyId,
		RelatedPartyId:   guarantor.PartyId,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_GUARANTOR,
	}
	_, err = svc.RegisterAssociations(ctx, assoc3Req)
	require.NoError(t, err)

	// Retrieve and verify all associations
	retrieveResp, err := svc.RetrieveAssociations(ctx, &pb.RetrieveAssociationsRequest{PartyId: primaryParty.PartyId})
	require.NoError(t, err)
	assert.Len(t, retrieveResp.Associations, 3, "Primary party should have 3 associations")

	// Verify relationship types
	partnerCount := 0
	guarantorCount := 0
	for _, assoc := range retrieveResp.Associations {
		if assoc.RelationshipType == pb.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER {
			partnerCount++
		}
		if assoc.RelationshipType == pb.RelationshipType_RELATIONSHIP_TYPE_GUARANTOR {
			guarantorCount++
		}
	}
	assert.Equal(t, 2, partnerCount, "Should have 2 business partner associations")
	assert.Equal(t, 1, guarantorCount, "Should have 1 guarantor association")
}

// TestWorkflow_ControlPartyStateTransitions verifies all valid state transitions:
// ACTIVE → RESTRICTED → ACTIVE → SUSPENDED (via SUSPEND) → TERMINATED
func TestWorkflow_ControlPartyStateTransitions(t *testing.T) {
	svc, _, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	// Register party (starts as ACTIVE)
	party := registerTestParty(t, ctx, svc, "State Transition Test")
	assert.Equal(t, pb.PartyStatus_PARTY_STATUS_ACTIVE, party.Status)

	// Transition: ACTIVE → RESTRICTED
	restrictReq := &pb.ControlPartyRequest{
		PartyId:       party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_RESTRICT,
		Reason:        "Pending KYC review",
		ActorId:       "COMPLIANCE-001",
	}
	restrictResp, err := svc.ControlParty(ctx, restrictReq)
	require.NoError(t, err)
	assert.Equal(t, pb.PartyStatus_PARTY_STATUS_RESTRICTED, restrictResp.Party.Status)

	// Transition: RESTRICTED → ACTIVE
	activateReq := &pb.ControlPartyRequest{
		PartyId:       party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
		Reason:        "KYC review completed",
		ActorId:       "COMPLIANCE-001",
	}
	activateResp, err := svc.ControlParty(ctx, activateReq)
	require.NoError(t, err)
	assert.Equal(t, pb.PartyStatus_PARTY_STATUS_ACTIVE, activateResp.Party.Status)

	// Transition: ACTIVE → SUSPENDED (via SUSPEND action)
	suspendReq := &pb.ControlPartyRequest{
		PartyId:       party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_SUSPEND,
		Reason:        "Suspicious activity detected",
		ActorId:       "FRAUD-TEAM-001",
	}
	suspendResp, err := svc.ControlParty(ctx, suspendReq)
	require.NoError(t, err)
	assert.Equal(t, pb.PartyStatus_PARTY_STATUS_SUSPENDED, suspendResp.Party.Status)

	// Transition: SUSPENDED → TERMINATED
	terminateReq := &pb.ControlPartyRequest{
		PartyId:       party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_TERMINATE,
		Reason:        "Fraud confirmed",
		ActorId:       "ADMIN-001",
	}
	terminateResp, err := svc.ControlParty(ctx, terminateReq)
	require.NoError(t, err)
	assert.Equal(t, pb.PartyStatus_PARTY_STATUS_TERMINATED, terminateResp.Party.Status)

	// Verify final state persists
	retrieveResp, err := svc.RetrieveParty(ctx, &pb.RetrievePartyRequest{PartyId: party.PartyId})
	require.NoError(t, err)
	assert.Equal(t, pb.PartyStatus_PARTY_STATUS_TERMINATED, retrieveResp.Party.Status)
}

// TestWorkflow_UpdateDemographicsIdempotency verifies that updating demographics
// multiple times with same data is idempotent and doesn't create duplicate rows.
func TestWorkflow_UpdateDemographicsIdempotency(t *testing.T) {
	svc, db, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	party := registerTestParty(t, ctx, svc, "Demographics Test Party")

	// Update demographics multiple times with same data
	demoReq := &pb.UpdateDemographicsRequest{
		PartyId:           party.PartyId,
		SocioEconomicData: "Upper-middle income",
		EmploymentHistory: "CEO of Acme Corp",
	}

	for i := 0; i < 3; i++ {
		_, err := svc.UpdateDemographics(ctx, demoReq)
		require.NoError(t, err, "Demographics update %d should succeed", i+1)
	}

	// Verify only ONE demographics record exists (UNIQUE constraint on party_id)
	var demoCount int64
	err := db.Raw(fmt.Sprintf(`
		SELECT COUNT(*) FROM %s.party_demographic WHERE party_id = ?
	`, pq.QuoteIdentifier(tenant.TenantID(testTenantID).SchemaName())), party.PartyId).Scan(&demoCount).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), demoCount, "Should have exactly 1 demographics record")

	// Verify final data is correct
	retrieveResp, err := svc.RetrieveDemographics(ctx, &pb.RetrieveDemographicsRequest{PartyId: party.PartyId})
	require.NoError(t, err)
	assert.Equal(t, "Upper-middle income", retrieveResp.SocioEconomicData)
}

// TestNegative_UpdateReferenceNonExistentParty verifies error handling
// when updating reference for a party that doesn't exist.
func TestNegative_UpdateReferenceNonExistentParty(t *testing.T) {
	svc, _, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	nonExistentPartyID := uuid.New().String()

	refReq := &pb.UpdateReferenceRequest{
		PartyId:      nonExistentPartyID,
		GovernmentId: "FAKE-ID",
	}

	resp, err := svc.UpdateReference(ctx, refReq)
	require.Error(t, err, "Update reference for non-existent party should fail")
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code(), "Should return NotFound error")
}

// TestNegative_ControlPartyInvalidTransition verifies that invalid state transitions
// are rejected (e.g., TERMINATED → ACTIVE should fail).
func TestNegative_ControlPartyInvalidTransition(t *testing.T) {
	svc, _, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	party := registerTestParty(t, ctx, svc, "Invalid Transition Test")

	// First terminate the party
	terminateReq := &pb.ControlPartyRequest{
		PartyId:       party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_TERMINATE,
		Reason:        "Test termination",
		ActorId:       "TEST-ACTOR",
	}
	_, err := svc.ControlParty(ctx, terminateReq)
	require.NoError(t, err)

	// Attempt to reactivate terminated party (should fail)
	activateReq := &pb.ControlPartyRequest{
		PartyId:       party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_ACTIVATE,
		Reason:        "Attempt to reactivate",
		ActorId:       "TEST-ACTOR",
	}
	resp, err := svc.ControlParty(ctx, activateReq)

	require.Error(t, err, "Reactivating terminated party should fail")
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.FailedPrecondition, st.Code(), "Should return FailedPrecondition error")
	assert.Contains(t, st.Message(), "invalid", "Error should mention invalid transition")
	assert.Contains(t, st.Message(), "transition", "Error should mention transition")
}

// TestNegative_RegisterAssociationNonExistentRelatedParty verifies error handling
// when registering an association with a related party that doesn't exist.
func TestNegative_RegisterAssociationNonExistentRelatedParty(t *testing.T) {
	svc, _, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	// Create a valid party
	party := registerTestParty(t, ctx, svc, "Valid Party")
	nonExistentPartyID := uuid.New().String()

	// Attempt to register association with non-existent related party
	assocReq := &pb.RegisterAssociationsRequest{
		PartyId:          party.PartyId,
		RelatedPartyId:   nonExistentPartyID,
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_SPOUSE,
	}

	resp, err := svc.RegisterAssociations(ctx, assocReq)
	require.Error(t, err, "Registering association with non-existent related party should fail")
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code(), "Should return NotFound error")
	assert.Contains(t, st.Message(), "related party not found", "Error should mention related party")
}

// TestNegative_RegisterAssociationCircularSameParty verifies error handling
// when attempting to create a circular association (party associated with itself).
func TestNegative_RegisterAssociationCircularSameParty(t *testing.T) {
	svc, _, ctx, cleanup := setupLifecycleIntegrationTest(t)
	defer cleanup()

	// Create a party
	party := registerTestParty(t, ctx, svc, "Self-Association Test")

	// Attempt to associate party with itself
	assocReq := &pb.RegisterAssociationsRequest{
		PartyId:          party.PartyId,
		RelatedPartyId:   party.PartyId, // Same party
		RelationshipType: pb.RelationshipType_RELATIONSHIP_TYPE_BUSINESS_PARTNER,
	}

	resp, err := svc.RegisterAssociations(ctx, assocReq)
	require.Error(t, err, "Registering self-association should fail")
	assert.Nil(t, resp)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code(), "Should return InvalidArgument error")
	assert.Contains(t, st.Message(), "circular", "Error should mention circular association")
}
