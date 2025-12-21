package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()

	db, cleanup := testdb.SetupPostgres(t, []interface{}{&TenantEntity{}, &ProvisioningStatusEntity{}})

	// Create audit_outbox table (required for GORM hooks)
	// Note: Tenant service uses string IDs (varchar(50)) for record_id
	err := db.Exec(`
		CREATE TABLE IF NOT EXISTS audit_outbox (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			table_name VARCHAR(100) NOT NULL,
			operation VARCHAR(10) NOT NULL CHECK (operation IN ('INSERT', 'UPDATE', 'DELETE')),
			record_id VARCHAR(50) NOT NULL,
			old_values TEXT,
			new_values TEXT,
			status VARCHAR(20) NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			retry_count INTEGER NOT NULL DEFAULT 0,
			last_error TEXT,
			changed_by VARCHAR(100),
			transaction_id VARCHAR(100),
			client_ip VARCHAR(45),
			user_agent TEXT
		)
	`).Error
	if err != nil {
		t.Fatalf("Failed to create audit_outbox table: %v", err)
	}

	return db, cleanup
}

func newTestTenant(id string) *domain.Tenant {
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

func TestRepository_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()
	tenant := newTestTenant("acme_bank")

	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Verify tenant was saved
	retrieved, err := repo.GetByID(ctx, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, tenant.ID.String(), retrieved.ID.String())
	assert.Equal(t, tenant.DisplayName, retrieved.DisplayName)
	assert.Equal(t, tenant.SettlementAsset, retrieved.SettlementAsset)
	assert.Equal(t, domain.StatusActive, retrieved.Status)
	assert.Equal(t, 1, retrieved.Version)
}

func TestRepository_Create_WithSubdomain(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()
	tenant := newTestTenant("acme_bank")
	tenant.Subdomain = "acme-bank.demo.meridian.io"

	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Verify subdomain was saved
	retrieved, err := repo.GetByID(ctx, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "acme-bank.demo.meridian.io", retrieved.Subdomain)
}

func TestRepository_Create_DuplicateTenant(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create first tenant
	tenant1 := newTestTenant("duplicate_tenant")
	err := repo.Create(ctx, tenant1)
	require.NoError(t, err)

	// Try to create duplicate
	tenant2 := newTestTenant("duplicate_tenant")
	err = repo.Create(ctx, tenant2)
	assert.True(t, errors.Is(err, ErrTenantExists), "Expected ErrTenantExists, got %v", err)
}

func TestRepository_Create_DuplicateSubdomain(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create first tenant with subdomain
	tenant1 := newTestTenant("tenant_one")
	tenant1.Subdomain = "shared-subdomain.demo.meridian.io"
	err := repo.Create(ctx, tenant1)
	require.NoError(t, err)

	// Create second tenant with same subdomain
	tenant2 := newTestTenant("tenant_two")
	tenant2.Subdomain = "shared-subdomain.demo.meridian.io"
	err = repo.Create(ctx, tenant2)
	assert.True(t, errors.Is(err, ErrSubdomainTaken), "Expected ErrSubdomainTaken, got %v", err)
}

func TestRepository_GetByID_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	tenantID, _ := tenant.NewTenantID("nonexistent_tenant")
	_, err := repo.GetByID(ctx, tenantID)
	assert.True(t, errors.Is(err, ErrTenantNotFound), "Expected ErrTenantNotFound, got %v", err)
}

func TestRepository_IsActive(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create active tenant
	tenant := newTestTenant("active_tenant")
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Check IsActive returns true
	active, err := repo.IsActive(ctx, tenant.ID)
	require.NoError(t, err)
	assert.True(t, active)
}

func TestRepository_IsActive_Suspended(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create and suspend tenant
	tenant := newTestTenant("suspended_tenant")
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	_, err = repo.UpdateStatus(ctx, tenant.ID, domain.StatusSuspended, tenant.Version)
	require.NoError(t, err)

	// Check IsActive returns false
	active, err := repo.IsActive(ctx, tenant.ID)
	require.NoError(t, err)
	assert.False(t, active)
}

func TestRepository_IsActive_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	tenantID, _ := tenant.NewTenantID("nonexistent_tenant")
	_, err := repo.IsActive(ctx, tenantID)
	assert.True(t, errors.Is(err, ErrTenantNotFound), "Expected ErrTenantNotFound, got %v", err)
}

func TestRepository_UpdateStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant
	tenant := newTestTenant("status_test_tenant")
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Update status to suspended
	updated, err := repo.UpdateStatus(ctx, tenant.ID, domain.StatusSuspended, 1)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusSuspended, updated.Status)
	assert.Equal(t, 2, updated.Version)
}

func TestRepository_UpdateStatus_Deprovisioned(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant
	tenant := newTestTenant("deprov_test_tenant")
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Update status to deprovisioned
	updated, err := repo.UpdateStatus(ctx, tenant.ID, domain.StatusDeprovisioned, 1)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDeprovisioned, updated.Status)
	assert.NotNil(t, updated.DeprovisionedAt)
}

func TestRepository_UpdateStatus_VersionConflict(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant
	tenant := newTestTenant("conflict_test_tenant")
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Update with wrong version
	_, err = repo.UpdateStatus(ctx, tenant.ID, domain.StatusSuspended, 999)
	assert.True(t, errors.Is(err, ErrVersionConflict), "Expected ErrVersionConflict, got %v", err)
}

func TestRepository_UpdateStatus_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	tenantID, _ := tenant.NewTenantID("nonexistent_tenant")
	_, err := repo.UpdateStatus(ctx, tenantID, domain.StatusSuspended, 1)
	assert.True(t, errors.Is(err, ErrTenantNotFound), "Expected ErrTenantNotFound, got %v", err)
}

func TestRepository_List(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create multiple tenants
	for i := 1; i <= 5; i++ {
		tenant := newTestTenant(string(rune('a'+i)) + "_tenant")
		err := repo.Create(ctx, tenant)
		require.NoError(t, err)
		// Small delay to ensure distinct created_at timestamps
		time.Sleep(10 * time.Millisecond)
	}

	// List all tenants
	tenants, nextToken, err := repo.List(ctx, nil, 10, "")
	require.NoError(t, err)
	assert.Len(t, tenants, 5)
	assert.Empty(t, nextToken)
}

func TestRepository_List_WithStatusFilter(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create 3 active and 2 suspended tenants
	for i := 1; i <= 3; i++ {
		tenant := newTestTenant("active_" + string(rune('a'+i)))
		err := repo.Create(ctx, tenant)
		require.NoError(t, err)
	}

	for i := 1; i <= 2; i++ {
		tenant := newTestTenant("suspended_" + string(rune('a'+i)))
		tenant.Status = domain.StatusSuspended
		err := repo.Create(ctx, tenant)
		require.NoError(t, err)
	}

	// List only active tenants
	activeStatus := domain.StatusActive
	tenants, _, err := repo.List(ctx, &activeStatus, 10, "")
	require.NoError(t, err)
	assert.Len(t, tenants, 3)

	for _, tenant := range tenants {
		assert.Equal(t, domain.StatusActive, tenant.Status)
	}
}

func TestRepository_List_Pagination(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create 10 tenants
	for i := 0; i < 10; i++ {
		tenant := newTestTenant("page_tenant_" + string(rune('a'+i)))
		err := repo.Create(ctx, tenant)
		require.NoError(t, err)
		time.Sleep(10 * time.Millisecond) // Ensure distinct timestamps
	}

	// Get first page of 3
	page1, nextToken, err := repo.List(ctx, nil, 3, "")
	require.NoError(t, err)
	assert.Len(t, page1, 3)
	assert.NotEmpty(t, nextToken)

	// Get second page
	page2, nextToken2, err := repo.List(ctx, nil, 3, nextToken)
	require.NoError(t, err)
	assert.Len(t, page2, 3)
	assert.NotEmpty(t, nextToken2)

	// Verify no overlap
	for _, p1Tenant := range page1 {
		for _, p2Tenant := range page2 {
			assert.NotEqual(t, p1Tenant.ID, p2Tenant.ID, "Pages should not overlap")
		}
	}
}

func TestRepository_GetAll(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create 5 tenants
	for i := 1; i <= 5; i++ {
		tenant := newTestTenant("getall_" + string(rune('a'+i)))
		err := repo.Create(ctx, tenant)
		require.NoError(t, err)
	}

	// Get all
	tenants, err := repo.GetAll(ctx)
	require.NoError(t, err)
	assert.Len(t, tenants, 5)
}

func TestRepository_Ping(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	err := repo.Ping(ctx)
	require.NoError(t, err)
}

func TestRepository_Create_WithMetadata(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	tenant := newTestTenant("metadata_tenant")
	tenant.Metadata = map[string]interface{}{
		"tier":         "enterprise",
		"max_accounts": float64(10000),
		"features":     []interface{}{"multi-currency", "batch-payments"},
	}

	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Verify metadata was saved
	retrieved, err := repo.GetByID(ctx, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "enterprise", retrieved.Metadata["tier"])
	assert.Equal(t, float64(10000), retrieved.Metadata["max_accounts"])
}

func TestRepository_FindProvisioningStatusByTenantID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant
	tenant := newTestTenant("test_tenant")
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Insert provisioning status records
	now := time.Now()
	completed := now.Add(5 * time.Minute)
	migrationVersion := "20251216000001"
	errorMsg := "connection timeout"

	statuses := []ProvisioningStatusEntity{
		{
			TenantID:         tenant.ID.String(),
			ServiceName:      "party",
			Status:           "completed",
			MigrationVersion: &migrationVersion,
			StartedAt:        &now,
			CompletedAt:      &completed,
		},
		{
			TenantID:    tenant.ID.String(),
			ServiceName: "account",
			Status:      "in_progress",
			StartedAt:   &now,
		},
		{
			TenantID:     tenant.ID.String(),
			ServiceName:  "transaction",
			Status:       "failed",
			ErrorMessage: &errorMsg,
			StartedAt:    &now,
			CompletedAt:  &completed,
		},
	}

	for _, status := range statuses {
		err := db.Create(&status).Error
		require.NoError(t, err)
	}

	// Query provisioning status
	results, err := repo.FindProvisioningStatusByTenantID(ctx, tenant.ID.String())
	require.NoError(t, err)
	assert.Len(t, results, 3)

	// Verify results are ordered by service_name
	assert.Equal(t, "account", results[0].ServiceName)
	assert.Equal(t, "in_progress", results[0].Status)
	assert.Nil(t, results[0].ErrorMessage)
	assert.NotNil(t, results[0].StartedAt)
	assert.Nil(t, results[0].CompletedAt)

	assert.Equal(t, "party", results[1].ServiceName)
	assert.Equal(t, "completed", results[1].Status)
	assert.Equal(t, migrationVersion, results[1].MigrationVersion)
	assert.NotNil(t, results[1].StartedAt)
	assert.NotNil(t, results[1].CompletedAt)

	assert.Equal(t, "transaction", results[2].ServiceName)
	assert.Equal(t, "failed", results[2].Status)
	assert.NotNil(t, results[2].ErrorMessage)
	assert.Equal(t, errorMsg, *results[2].ErrorMessage)
}

func TestRepository_FindProvisioningStatusByTenantID_EmptyResult(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant without provisioning status records
	tenant := newTestTenant("test_tenant")
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Query provisioning status
	results, err := repo.FindProvisioningStatusByTenantID(ctx, tenant.ID.String())
	require.NoError(t, err)
	assert.NotNil(t, results)
	assert.Len(t, results, 0)
}

func TestRepository_FindProvisioningStatusByTenantID_VariousStatuses(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant
	tenant := newTestTenant("test_tenant")
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Insert records with different status values
	statusValues := []string{"pending", "in_progress", "completed", "failed"}
	for i, statusValue := range statusValues {
		entity := ProvisioningStatusEntity{
			TenantID:    tenant.ID.String(),
			ServiceName: "service_" + string(rune('a'+i)),
			Status:      statusValue,
		}
		err := db.Create(&entity).Error
		require.NoError(t, err)
	}

	// Query and verify all statuses
	results, err := repo.FindProvisioningStatusByTenantID(ctx, tenant.ID.String())
	require.NoError(t, err)
	assert.Len(t, results, 4)

	// Verify each status value
	statusMap := make(map[string]string)
	for _, result := range results {
		statusMap[result.ServiceName] = result.Status
	}

	assert.Equal(t, "pending", statusMap["service_a"])
	assert.Equal(t, "in_progress", statusMap["service_b"])
	assert.Equal(t, "completed", statusMap["service_c"])
	assert.Equal(t, "failed", statusMap["service_d"])
}

func TestRepository_FindProvisioningStatusByTenantID_NullHandling(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant
	tenant := newTestTenant("test_tenant")
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Insert record with all optional fields as NULL
	entity := ProvisioningStatusEntity{
		TenantID:         tenant.ID.String(),
		ServiceName:      "party",
		Status:           "pending",
		MigrationVersion: nil,
		ErrorMessage:     nil,
		StartedAt:        nil,
		CompletedAt:      nil,
	}
	err = db.Create(&entity).Error
	require.NoError(t, err)

	// Query and verify null handling
	results, err := repo.FindProvisioningStatusByTenantID(ctx, tenant.ID.String())
	require.NoError(t, err)
	assert.Len(t, results, 1)

	assert.Equal(t, "party", results[0].ServiceName)
	assert.Equal(t, "pending", results[0].Status)
	assert.Equal(t, "", results[0].MigrationVersion)
	assert.Nil(t, results[0].ErrorMessage)
	assert.Nil(t, results[0].StartedAt)
	assert.Nil(t, results[0].CompletedAt)
}
