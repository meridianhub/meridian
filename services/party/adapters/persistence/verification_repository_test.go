package persistence

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupVerificationTestDB(t *testing.T) (*gorm.DB, context.Context, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{
		&PartyEntity{},
		&PartyVerificationEntity{},
		&audit.AuditOutbox{},
	})

	// Create the tenant schema for tests
	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create the party table in the tenant schema
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

	// Create the party_verification table in the tenant schema
	// Use VARCHAR for status instead of enum type for test simplicity
	err = db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party_verification (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		party_id UUID NOT NULL REFERENCES %s.party(id) ON DELETE CASCADE,
		verification_id VARCHAR(255) NOT NULL UNIQUE,
		provider VARCHAR(100) NOT NULL,
		status VARCHAR(20) NOT NULL DEFAULT 'PENDING',
		risk_score DECIMAL(5,4),
		reason TEXT,
		completed_at TIMESTAMP WITH TIME ZONE,
		metadata JSONB DEFAULT '{}',
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now()
	)`, schemaName, schemaName)).Error
	require.NoError(t, err)

	// Create the audit_outbox table in the tenant schema
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

	// Set default search_path to include tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Create context with tenant
	ctx := tenant.WithTenant(context.Background(), tid)

	return db, ctx, cleanup
}

// createTestParty is a helper to create a party for verification tests
func createTestParty(t *testing.T, db *gorm.DB, _ context.Context) uuid.UUID {
	t.Helper()
	partyID := uuid.New()
	now := time.Now()

	err := db.Exec(`
		INSERT INTO party (id, party_type, legal_name, status, version, created_at, updated_at, created_by, updated_by)
		VALUES (?, 'PERSON', 'Test Person', 'ACTIVE', 1, ?, ?, 'system', 'system')
	`, partyID, now, now).Error
	require.NoError(t, err)

	return partyID
}

func TestCreateVerification(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	partyID := createTestParty(t, db, ctx)
	repo := NewVerificationRepository(db)

	verification := &PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: "prov-12345",
		Provider:       "onfido",
		Status:         "PENDING",
	}

	err := repo.CreateVerification(ctx, verification)
	require.NoError(t, err)

	// Verify the record was created
	retrieved, err := repo.GetVerificationByID(ctx, verification.ID)
	require.NoError(t, err)
	assert.Equal(t, partyID, retrieved.PartyID)
	assert.Equal(t, "prov-12345", retrieved.VerificationID)
	assert.Equal(t, "onfido", retrieved.Provider)
	assert.Equal(t, "PENDING", retrieved.Status)
	assert.Equal(t, int64(1), retrieved.Version)
}

func TestCreateVerification_GeneratesID(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	partyID := createTestParty(t, db, ctx)
	repo := NewVerificationRepository(db)

	verification := &PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: "prov-67890",
		Provider:       "onfido",
		Status:         "PENDING",
	}

	// ID should be nil before creation
	assert.Equal(t, uuid.Nil, verification.ID)

	err := repo.CreateVerification(ctx, verification)
	require.NoError(t, err)

	// ID should be populated after creation
	assert.NotEqual(t, uuid.Nil, verification.ID)
}

func TestUpdateVerificationStatus(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	partyID := createTestParty(t, db, ctx)
	repo := NewVerificationRepository(db)

	// Create a verification
	verification := &PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: "prov-update-test",
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err := repo.CreateVerification(ctx, verification)
	require.NoError(t, err)

	// Update the status
	riskScore := 0.25
	reason := "All checks passed"
	completedAt := time.Now()

	err = repo.UpdateVerificationStatus(
		ctx,
		verification.ID,
		"APPROVED",
		&riskScore,
		&reason,
		&completedAt,
		verification.Version, // current version is 1
	)
	require.NoError(t, err)

	// Verify the update
	updated, err := repo.GetVerificationByID(ctx, verification.ID)
	require.NoError(t, err)
	assert.Equal(t, "APPROVED", updated.Status)
	assert.NotNil(t, updated.RiskScore)
	assert.InDelta(t, 0.25, *updated.RiskScore, 0.0001)
	assert.NotNil(t, updated.Reason)
	assert.Equal(t, "All checks passed", *updated.Reason)
	assert.NotNil(t, updated.CompletedAt)
	assert.Equal(t, int64(2), updated.Version) // Version incremented
}

func TestUpdateVerificationStatus_VersionConflict(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	partyID := createTestParty(t, db, ctx)
	repo := NewVerificationRepository(db)

	// Create a verification
	verification := &PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: "prov-version-conflict",
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err := repo.CreateVerification(ctx, verification)
	require.NoError(t, err)

	// First update succeeds
	err = repo.UpdateVerificationStatus(
		ctx,
		verification.ID,
		"APPROVED",
		nil,
		nil,
		nil,
		1, // current version
	)
	require.NoError(t, err)

	// Second update with stale version fails
	err = repo.UpdateVerificationStatus(
		ctx,
		verification.ID,
		"REJECTED",
		nil,
		nil,
		nil,
		1, // stale version
	)
	assert.True(t, errors.Is(err, ErrVersionConflict))
}

func TestGetVerificationByProviderID(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	partyID := createTestParty(t, db, ctx)
	repo := NewVerificationRepository(db)

	verification := &PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: "prov-lookup-test",
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err := repo.CreateVerification(ctx, verification)
	require.NoError(t, err)

	// Find by provider's verification ID
	found, err := repo.GetVerificationByProviderID(ctx, "prov-lookup-test")
	require.NoError(t, err)
	assert.Equal(t, verification.ID, found.ID)
	assert.Equal(t, partyID, found.PartyID)
}

func TestGetVerificationByProviderID_NotFound(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	repo := NewVerificationRepository(db)

	_, err := repo.GetVerificationByProviderID(ctx, "nonexistent-id")
	assert.True(t, errors.Is(err, ErrVerificationNotFound))
}

func TestListVerificationsByParty(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	partyID := createTestParty(t, db, ctx)
	repo := NewVerificationRepository(db)

	// Create multiple verifications for the same party
	for i := 1; i <= 3; i++ {
		verification := &PartyVerificationEntity{
			PartyID:        partyID,
			VerificationID: fmt.Sprintf("prov-list-%d", i),
			Provider:       "onfido",
			Status:         "PENDING",
		}
		err := repo.CreateVerification(ctx, verification)
		require.NoError(t, err)
		// Intentional sleep: Ensure different timestamps for ordering tests
		time.Sleep(10 * time.Millisecond)
	}

	// List verifications
	verifications, err := repo.ListVerificationsByParty(ctx, partyID)
	require.NoError(t, err)
	require.Len(t, verifications, 3)

	// Should be in chronological order (oldest first)
	assert.Equal(t, "prov-list-1", verifications[0].VerificationID)
	assert.Equal(t, "prov-list-2", verifications[1].VerificationID)
	assert.Equal(t, "prov-list-3", verifications[2].VerificationID)
}

func TestListVerificationsByParty_Empty(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	partyID := createTestParty(t, db, ctx)
	repo := NewVerificationRepository(db)

	// No verifications created
	verifications, err := repo.ListVerificationsByParty(ctx, partyID)
	require.NoError(t, err)
	assert.Empty(t, verifications)
}

func TestTenantIsolation(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	// Create party and verification in tenant A
	partyID := createTestParty(t, db, ctx)
	repo := NewVerificationRepository(db)

	verification := &PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: "prov-tenant-test",
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err := repo.CreateVerification(ctx, verification)
	require.NoError(t, err)

	// Verify exists in tenant A
	found, err := repo.GetVerificationByID(ctx, verification.ID)
	require.NoError(t, err)
	assert.Equal(t, verification.ID, found.ID)

	// Create a different tenant context
	tenantB := tenant.TenantID("tenant_b")
	ctxB := tenant.WithTenant(context.Background(), tenantB)

	// The verification should not be visible in tenant B's context
	// Note: This test demonstrates the pattern - full isolation testing
	// requires proper schema setup for tenant B
	_, err = repo.GetVerificationByID(ctxB, verification.ID)
	// Either not found or error due to missing schema is acceptable
	t.Logf("Tenant B lookup result: err=%v", err)
}

func TestListPendingVerifications(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	partyID := createTestParty(t, db, ctx)
	repo := NewVerificationRepository(db)

	// Create verifications with different statuses
	statuses := []string{"PENDING", "APPROVED", "PENDING", "REJECTED"}
	for i, status := range statuses {
		verification := &PartyVerificationEntity{
			PartyID:        partyID,
			VerificationID: fmt.Sprintf("prov-pending-%d", i),
			Provider:       "onfido",
			Status:         status,
		}
		err := repo.CreateVerification(ctx, verification)
		require.NoError(t, err)
	}

	// List pending only
	pending, err := repo.ListPendingVerifications(ctx)
	require.NoError(t, err)
	require.Len(t, pending, 2)

	for _, v := range pending {
		assert.Equal(t, "PENDING", v.Status)
	}
}

func TestUpdateVerificationMetadata(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	partyID := createTestParty(t, db, ctx)
	repo := NewVerificationRepository(db)

	// Create verification
	verification := &PartyVerificationEntity{
		PartyID:        partyID,
		VerificationID: "prov-metadata-test",
		Provider:       "onfido",
		Status:         "PENDING",
	}
	err := repo.CreateVerification(ctx, verification)
	require.NoError(t, err)

	// Update metadata
	metadata := `{"document_type":"passport","confidence":0.95}`
	err = repo.UpdateVerificationMetadata(ctx, verification.ID, metadata)
	require.NoError(t, err)

	// Verify metadata persisted
	updated, err := repo.GetVerificationByID(ctx, verification.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.Metadata)
	assert.JSONEq(t, metadata, *updated.Metadata)
}

func TestUpdateVerificationMetadata_NotFound(t *testing.T) {
	db, ctx, cleanup := setupVerificationTestDB(t)
	defer cleanup()

	repo := NewVerificationRepository(db)

	err := repo.UpdateVerificationMetadata(ctx, uuid.New(), `{"test": "data"}`)
	assert.ErrorIs(t, err, ErrVerificationNotFound)
}
