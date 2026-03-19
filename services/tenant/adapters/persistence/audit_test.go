package persistence

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupTestDBWithAudit creates a test database with GORM for testing
// and sets up the audit_outbox table (via DDL) required for GORM hooks.
func setupTestDBWithAudit(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	db, _, cleanup := testdb.SetupTestDB(t,
		testdb.WithModels(&TenantEntity{}),
		testdb.WithAuditTables(),
	)
	return db, cleanup
}

func newAuditTestTenant(id string) *domain.Tenant {
	tenantID, _ := tenant.NewTenantID(id)
	return &domain.Tenant{
		ID:              tenantID,
		DisplayName:     "Test Tenant " + id,
		SettlementAsset: "GBP",
		Status:          domain.StatusActive,
		CreatedAt:       time.Now(),
		Metadata:        map[string]interface{}{"tier": "free"},
		Version:         1,
	}
}

// TestAuditOutbox_AtomicCommit verifies that audit outbox entry is created atomically
// with the tenant operation within the same transaction.
//
// Critical Guarantee: Atomicity - Audit intent committed with business operation
func TestAuditOutbox_AtomicCommit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()
	tenantObj := newAuditTestTenant("audit_atomic_tenant")

	err := repo.Create(ctx, tenantObj)
	require.NoError(t, err, "Failed to create tenant")

	// Verify outbox entry exists
	var outbox audit.AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ?", tenantObj.ID.String()).
		First(&outbox).Error
	require.NoError(t, err, "Audit outbox entry should exist")

	// Verify outbox content
	assert.Equal(t, "tenant", outbox.Table, "Table name should be 'tenant'")
	assert.Equal(t, "INSERT", outbox.Operation, "Operation should be 'INSERT'")
	assert.Equal(t, "pending", outbox.Status, "Status should be 'pending'")
	assert.NotEmpty(t, outbox.NewValues, "NewValues should contain tenant data")
	assert.Empty(t, outbox.OldValues, "OldValues should be empty for INSERT")
}

// TestAuditOutbox_RollbackOnBusinessFailure verifies that audit outbox entry is
// rolled back when the business transaction fails.
//
// Critical Guarantee: Atomicity - Outbox entry rolled back with failed business operation
func TestAuditOutbox_RollbackOnBusinessFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// Force transaction failure
	err := db.Transaction(func(tx *gorm.DB) error {
		entity := &TenantEntity{
			ID:              "rollback_test_tenant",
			DisplayName:     "Rollback Test Tenant",
			SettlementAsset: "GBP",
			Status:          string(domain.StatusActive),
			Version:         1,
		}

		err := tx.Create(entity).Error
		require.NoError(t, err, "Entity creation should succeed within transaction")

		// Verify outbox entry exists within transaction
		var count int64
		tx.Table("audit_outbox").
			Where("record_id = ?", entity.ID).
			Count(&count)
		assert.Equal(t, int64(1), count, "Outbox entry should exist within transaction")

		// Force rollback
		return gorm.ErrInvalidTransaction
	})
	require.Error(t, err, "Transaction should fail")

	// Verify outbox entry was rolled back
	var count int64
	db.Table("audit_outbox").Count(&count)
	assert.Equal(t, int64(0), count, "Outbox should be empty after rollback")

	// Verify tenant was also rolled back
	var tenantCount int64
	db.Model(&TenantEntity{}).Count(&tenantCount)
	assert.Equal(t, int64(0), tenantCount, "Tenant should not exist after rollback")
}

// TestAuditOutbox_TenantRegistration verifies that tenant registration is audited
// with ID, display_name, and settlement_asset.
func TestAuditOutbox_TenantRegistration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()
	tenantObj := newAuditTestTenant("registration_audit_tenant")
	tenantObj.DisplayName = "Acme Corporation"
	tenantObj.SettlementAsset = "USD"

	err := repo.Create(ctx, tenantObj)
	require.NoError(t, err, "Failed to create tenant")

	// Verify INSERT audit
	var insertAudit audit.AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", tenantObj.ID.String(), "INSERT").
		First(&insertAudit).Error
	require.NoError(t, err, "INSERT audit should exist")

	// Verify audit captures tenant registration details
	var newValues map[string]interface{}
	err = json.Unmarshal([]byte(insertAudit.NewValues), &newValues)
	require.NoError(t, err, "Failed to unmarshal new values")

	assert.Equal(t, "registration_audit_tenant", newValues["ID"], "ID should be captured")
	assert.Equal(t, "Acme Corporation", newValues["DisplayName"], "DisplayName should be captured")
	assert.Equal(t, "USD", newValues["SettlementAsset"], "SettlementAsset should be captured")
}

// TestAuditOutbox_StatusTransitions verifies that tenant status transitions
// (active→suspended→deprovisioned) are audited when using GORM Save().
// Note: Repository.UpdateStatus uses map-based updates which bypass GORM hooks.
// This test validates the hooks work correctly with model-based updates.
func TestAuditOutbox_StatusTransitions(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// Create tenant entity directly
	entity := &TenantEntity{
		ID:              "status_transition_tenant",
		DisplayName:     "Status Transition Test",
		SettlementAsset: "GBP",
		Status:          string(domain.StatusActive),
		Version:         1,
	}
	err := db.Create(entity).Error
	require.NoError(t, err, "Failed to create tenant")

	// active → suspended (using Save which triggers hooks)
	entity.Status = string(domain.StatusSuspended)
	entity.Version = 2
	err = db.Save(entity).Error
	require.NoError(t, err, "Failed to suspend tenant")

	// Verify UPDATE audit for suspension
	var suspendAudit audit.AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", entity.ID, "UPDATE").
		Order("created_at DESC").
		First(&suspendAudit).Error
	require.NoError(t, err, "UPDATE audit should exist for suspension")

	// Verify old and new status
	var oldValues, newValues map[string]interface{}
	err = json.Unmarshal([]byte(suspendAudit.OldValues), &oldValues)
	require.NoError(t, err, "Failed to unmarshal old values")
	err = json.Unmarshal([]byte(suspendAudit.NewValues), &newValues)
	require.NoError(t, err, "Failed to unmarshal new values")

	assert.Equal(t, "active", oldValues["Status"], "Old status should be 'active'")
	assert.Equal(t, "suspended", newValues["Status"], "New status should be 'suspended'")

	// suspended → deprovisioned
	entity.Status = string(domain.StatusDeprovisioned)
	now := time.Now()
	entity.DeprovisionedAt = &now
	entity.Version = 3
	err = db.Save(entity).Error
	require.NoError(t, err, "Failed to deprovision tenant")

	// Verify total UPDATE count (should have 2 updates)
	var updateCount int64
	db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", entity.ID, "UPDATE").
		Count(&updateCount)
	assert.Equal(t, int64(2), updateCount, "Should have 2 UPDATE audit records")
}

// TestAuditOutbox_MetadataChanges verifies that metadata changes are captured in audit trail.
func TestAuditOutbox_MetadataChanges(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// Create tenant with initial metadata
	entity := &TenantEntity{
		ID:              "metadata_change_tenant",
		DisplayName:     "Metadata Change Test",
		SettlementAsset: "GBP",
		Status:          string(domain.StatusActive),
		Metadata:        JSONMap{"tier": "free", "max_accounts": float64(100)},
		Version:         1,
	}
	err := db.Create(entity).Error
	require.NoError(t, err, "Failed to create tenant")

	// Update metadata
	entity.Metadata = JSONMap{"tier": "enterprise", "max_accounts": float64(10000)}
	entity.Version = 2
	err = db.Save(entity).Error
	require.NoError(t, err, "Failed to update tenant metadata")

	// Verify UPDATE audit
	var updateAudit audit.AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", entity.ID, "UPDATE").
		First(&updateAudit).Error
	require.NoError(t, err, "UPDATE audit should exist")

	// Verify metadata change is captured
	var oldValues, newValues map[string]interface{}
	err = json.Unmarshal([]byte(updateAudit.OldValues), &oldValues)
	require.NoError(t, err, "Failed to unmarshal old values")
	err = json.Unmarshal([]byte(updateAudit.NewValues), &newValues)
	require.NoError(t, err, "Failed to unmarshal new values")

	oldMetadataRaw, ok := oldValues["Metadata"]
	require.True(t, ok, "Metadata should exist in old values")
	oldMetadata, ok := oldMetadataRaw.(map[string]interface{})
	require.True(t, ok, "Metadata should be a map in old values")

	newMetadataRaw, ok := newValues["Metadata"]
	require.True(t, ok, "Metadata should exist in new values")
	newMetadata, ok := newMetadataRaw.(map[string]interface{})
	require.True(t, ok, "Metadata should be a map in new values")

	assert.Equal(t, "free", oldMetadata["tier"], "Old tier should be 'free'")
	assert.Equal(t, "enterprise", newMetadata["tier"], "New tier should be 'enterprise'")
}

// TestAuditOutbox_DeprovisionedAtTimestamp verifies that DeprovisionedAt timestamp is audited.
// Note: Uses direct GORM Save() to trigger hooks, as Repository.UpdateStatus bypasses them.
func TestAuditOutbox_DeprovisionedAtTimestamp(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// Create tenant entity directly
	entity := &TenantEntity{
		ID:              "deprovision_timestamp_tenant",
		DisplayName:     "Deprovision Timestamp Test",
		SettlementAsset: "GBP",
		Status:          string(domain.StatusActive),
		Version:         1,
	}
	err := db.Create(entity).Error
	require.NoError(t, err, "Failed to create tenant")

	// Deprovision tenant using Save (triggers hooks)
	entity.Status = string(domain.StatusDeprovisioned)
	now := time.Now()
	entity.DeprovisionedAt = &now
	entity.Version = 2
	err = db.Save(entity).Error
	require.NoError(t, err, "Failed to deprovision tenant")

	// Verify UPDATE audit captures DeprovisionedAt
	var updateAudit audit.AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", entity.ID, "UPDATE").
		First(&updateAudit).Error
	require.NoError(t, err, "UPDATE audit should exist")

	var newValues map[string]interface{}
	err = json.Unmarshal([]byte(updateAudit.NewValues), &newValues)
	require.NoError(t, err, "Failed to unmarshal new values")

	assert.NotNil(t, newValues["DeprovisionedAt"], "DeprovisionedAt should be captured in audit")
}

// TestAuditOutbox_CapturesChangedBy verifies that the audit records capture
// the user who made the change from the context.
func TestAuditOutbox_CapturesChangedBy(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()
	tenantObj := newAuditTestTenant("changed_by_audit_tenant")

	err := repo.Create(ctx, tenantObj)
	require.NoError(t, err, "Failed to create tenant")

	// Verify audit captures changed_by
	var outbox audit.AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ?", tenantObj.ID.String()).
		First(&outbox).Error
	require.NoError(t, err, "Audit outbox entry should exist")

	// Should default to audit.DefaultAuditUser ("system") when no JWT context present
	require.NotNil(t, outbox.ChangedBy, "ChangedBy should not be nil")
	assert.Equal(t, audit.DefaultAuditUser, *outbox.ChangedBy, "ChangedBy should default to system user")
}

// TestAuditOutbox_CapturesInsertUpdateDelete verifies that all operations
// (INSERT, UPDATE, DELETE) are captured in the audit outbox.
func TestAuditOutbox_CapturesInsertUpdateDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	db, cleanup := setupTestDBWithAudit(t)
	defer cleanup()

	// INSERT
	entity := &TenantEntity{
		ID:              "crud_audit_tenant",
		DisplayName:     "CRUD Audit Test",
		SettlementAsset: "GBP",
		Status:          string(domain.StatusActive),
		Version:         1,
	}
	err := db.Create(entity).Error
	require.NoError(t, err, "Failed to create tenant")

	// Verify INSERT audit
	var insertAudit audit.AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", entity.ID, "INSERT").
		First(&insertAudit).Error
	require.NoError(t, err, "INSERT audit should exist")
	assert.Equal(t, "INSERT", insertAudit.Operation)
	assert.NotEmpty(t, insertAudit.NewValues)
	assert.Empty(t, insertAudit.OldValues)

	// UPDATE
	entity.DisplayName = "Updated CRUD Audit Test"
	entity.Status = string(domain.StatusSuspended)
	entity.Version = 2
	err = db.Save(entity).Error
	require.NoError(t, err, "Failed to update tenant")

	// Verify UPDATE audit
	var updateAudit audit.AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", entity.ID, "UPDATE").
		First(&updateAudit).Error
	require.NoError(t, err, "UPDATE audit should exist")
	assert.Equal(t, "UPDATE", updateAudit.Operation)
	assert.NotEmpty(t, updateAudit.OldValues, "UPDATE should capture old values")
	assert.NotEmpty(t, updateAudit.NewValues, "UPDATE should capture new values")

	// DELETE
	err = db.Delete(entity).Error
	require.NoError(t, err, "Failed to delete tenant")

	// Verify DELETE audit
	var deleteAudit audit.AuditOutbox
	err = db.Table("audit_outbox").
		Where("record_id = ? AND operation = ?", entity.ID, "DELETE").
		First(&deleteAudit).Error
	require.NoError(t, err, "DELETE audit should exist")
	assert.Equal(t, "DELETE", deleteAudit.Operation)
	assert.NotEmpty(t, deleteAudit.OldValues, "DELETE should capture old values")
	assert.Empty(t, deleteAudit.NewValues, "DELETE should have empty new values")

	// Verify total audit count
	var totalCount int64
	db.Table("audit_outbox").
		Where("record_id = ?", entity.ID).
		Count(&totalCount)
	assert.Equal(t, int64(3), totalCount, "Should have exactly 3 audit records (INSERT, UPDATE, DELETE)")
}
