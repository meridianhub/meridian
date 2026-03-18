package persistence

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/tenant/domain"
	dbpkg "github.com/meridianhub/meridian/shared/platform/db"
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

func TestRepository_Create_DuplicateSlug(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create first tenant with slug
	tenant1 := newTestTenant("tenant_one")
	tenant1.Slug = "shared-slug"
	err := repo.Create(ctx, tenant1)
	require.NoError(t, err)

	// Create second tenant with same slug
	tenant2 := newTestTenant("tenant_two")
	tenant2.Slug = "shared-slug"
	err = repo.Create(ctx, tenant2)
	assert.True(t, errors.Is(err, ErrSlugTaken), "Expected ErrSlugTaken, got %v", err)
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

func TestRepository_GetBySlug(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant with slug
	testTenant := newTestTenant("acme_bank")
	testTenant.Slug = "acme-bank"
	testTenant.DisplayName = "ACME Bank"
	testTenant.SettlementAsset = "USD"

	err := repo.Create(ctx, testTenant)
	require.NoError(t, err)

	// Retrieve by slug
	retrieved, err := repo.GetBySlug(ctx, "acme-bank")
	require.NoError(t, err)
	assert.Equal(t, testTenant.ID.String(), retrieved.ID.String())
	assert.Equal(t, "acme-bank", retrieved.Slug)
	assert.Equal(t, "ACME Bank", retrieved.DisplayName)
	assert.Equal(t, "USD", retrieved.SettlementAsset)
	assert.Equal(t, domain.StatusActive, retrieved.Status)
}

func TestRepository_GetBySlug_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	_, err := repo.GetBySlug(ctx, "nonexistent-slug")
	assert.True(t, errors.Is(err, ErrTenantNotFound), "Expected ErrTenantNotFound, got %v", err)
}

func TestRepository_GetBySlug_EmptyString(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Empty slug should return ErrTenantNotFound immediately (fail-fast)
	_, err := repo.GetBySlug(ctx, "")
	assert.True(t, errors.Is(err, ErrTenantNotFound), "Expected ErrTenantNotFound for empty slug, got %v", err)
}

func TestRepository_GetBySlug_ReturnsAllFields(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant with all fields populated
	testTenant := newTestTenant("full_tenant")
	testTenant.Slug = "full-tenant-slug"
	testTenant.Subdomain = "full-tenant.demo.meridian.io"
	testTenant.DisplayName = "Full Tenant Inc."
	testTenant.SettlementAsset = "EUR"
	testTenant.Metadata = map[string]interface{}{
		"tier":     "enterprise",
		"features": []interface{}{"batch", "multi-currency"},
	}

	err := repo.Create(ctx, testTenant)
	require.NoError(t, err)

	// Retrieve by slug and verify all fields
	retrieved, err := repo.GetBySlug(ctx, "full-tenant-slug")
	require.NoError(t, err)

	assert.Equal(t, testTenant.ID.String(), retrieved.ID.String())
	assert.Equal(t, "full-tenant-slug", retrieved.Slug)
	assert.Equal(t, "full-tenant.demo.meridian.io", retrieved.Subdomain)
	assert.Equal(t, "Full Tenant Inc.", retrieved.DisplayName)
	assert.Equal(t, "EUR", retrieved.SettlementAsset)
	assert.Equal(t, domain.StatusActive, retrieved.Status)
	assert.Equal(t, "enterprise", retrieved.Metadata["tier"])
	assert.NotZero(t, retrieved.CreatedAt)
	assert.Equal(t, 1, retrieved.Version)
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
		time.Sleep(10 * time.Millisecond) //nolint:forbidigo // ensures distinct timestamps for DB ordering test
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
		time.Sleep(10 * time.Millisecond) //nolint:forbidigo // ensures distinct timestamps for DB ordering test
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

func TestRepository_Create_WithSlug(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()
	tenant := newTestTenant("acme_bank")
	tenant.Slug = "acme-bank"

	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Verify slug was saved
	retrieved, err := repo.GetByID(ctx, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "acme-bank", retrieved.Slug)
}

func TestRepository_Create_WithoutSlug(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()
	tenant := newTestTenant("acme_bank")
	tenant.Slug = "" // Explicitly empty

	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// Verify slug is empty
	retrieved, err := repo.GetByID(ctx, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "", retrieved.Slug)
}

func TestAdapterMapping_SlugRoundTrip(t *testing.T) {
	// Test toEntity with non-empty slug
	domainTenant := newTestTenant("test_tenant")
	domainTenant.Slug = "test-slug"

	entity := toEntity(domainTenant)
	require.NotNil(t, entity.Slug, "entity.Slug should not be nil for non-empty domain.Slug")
	assert.Equal(t, "test-slug", *entity.Slug)

	// Test toDomain with non-nil slug
	backToDomain, err := toDomain(entity)
	require.NoError(t, err)
	assert.Equal(t, "test-slug", backToDomain.Slug)
}

func TestAdapterMapping_EmptySlugMapsToNil(t *testing.T) {
	// Test toEntity with empty slug
	domainTenant := newTestTenant("test_tenant")
	domainTenant.Slug = ""

	entity := toEntity(domainTenant)
	assert.Nil(t, entity.Slug, "entity.Slug should be nil for empty domain.Slug")

	// Test toDomain with nil slug
	backToDomain, err := toDomain(entity)
	require.NoError(t, err)
	assert.Equal(t, "", backToDomain.Slug)
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
	assert.Equal(t, domain.ServiceStatusInProgress, results[0].Status)
	assert.Nil(t, results[0].ErrorMessage)
	assert.NotNil(t, results[0].StartedAt)
	assert.Nil(t, results[0].CompletedAt)

	assert.Equal(t, "party", results[1].ServiceName)
	assert.Equal(t, domain.ServiceStatusCompleted, results[1].Status)
	assert.Equal(t, migrationVersion, results[1].MigrationVersion)
	assert.NotNil(t, results[1].StartedAt)
	assert.NotNil(t, results[1].CompletedAt)

	assert.Equal(t, "transaction", results[2].ServiceName)
	assert.Equal(t, domain.ServiceStatusFailed, results[2].Status)
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
	statusMap := make(map[string]domain.ServiceProvisioningStatus)
	for _, result := range results {
		statusMap[result.ServiceName] = result.Status
	}

	assert.Equal(t, domain.ServiceStatusPending, statusMap["service_a"])
	assert.Equal(t, domain.ServiceStatusInProgress, statusMap["service_b"])
	assert.Equal(t, domain.ServiceStatusCompleted, statusMap["service_c"])
	assert.Equal(t, domain.ServiceStatusFailed, statusMap["service_d"])
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
		Status:           string(domain.ServiceStatusPending),
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
	assert.Equal(t, domain.ServiceStatusPending, results[0].Status)
	assert.Equal(t, "", results[0].MigrationVersion)
	assert.Nil(t, results[0].ErrorMessage)
	assert.Nil(t, results[0].StartedAt)
	assert.Nil(t, results[0].CompletedAt)
}

func TestRepository_IsSlugAvailable_Available(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Check availability of a slug that doesn't exist
	available, err := repo.IsSlugAvailable(ctx, "available-slug")
	require.NoError(t, err)
	assert.True(t, available, "Expected slug to be available")
}

func TestRepository_IsSlugAvailable_Taken(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant with a slug
	testTenant := newTestTenant("acme_bank")
	testTenant.Slug = "taken-slug"
	err := repo.Create(ctx, testTenant)
	require.NoError(t, err)

	// Check availability of the taken slug
	available, err := repo.IsSlugAvailable(ctx, "taken-slug")
	require.NoError(t, err)
	assert.False(t, available, "Expected slug to be taken")
}

func TestRepository_IsSlugAvailable_Integration(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant with slug "test-slug"
	testTenant := newTestTenant("test_tenant")
	testTenant.Slug = "test-slug"
	err := repo.Create(ctx, testTenant)
	require.NoError(t, err)

	// Verify IsSlugAvailable("test-slug") returns false
	available, err := repo.IsSlugAvailable(ctx, "test-slug")
	require.NoError(t, err)
	assert.False(t, available, "Expected 'test-slug' to be unavailable after tenant creation")

	// Verify IsSlugAvailable("new-slug") returns true
	available, err = repo.IsSlugAvailable(ctx, "new-slug")
	require.NoError(t, err)
	assert.True(t, available, "Expected 'new-slug' to be available")
}

func TestRepository_IsSlugAvailable_EmptyString(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Empty slug is invalid input, should return false (not available)
	available, err := repo.IsSlugAvailable(ctx, "")
	require.NoError(t, err)
	assert.False(t, available, "Expected empty slug to be unavailable (invalid input)")
}

func TestRepository_SlugLookup_CaseInsensitive(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenant with lowercase slug (as stored in DB)
	testTenant := newTestTenant("acme_bank")
	testTenant.Slug = "acme-bank"
	testTenant.DisplayName = "ACME Bank"
	err := repo.Create(ctx, testTenant)
	require.NoError(t, err)

	testCases := []struct {
		name     string
		slugCase string
	}{
		{"uppercase", "ACME-BANK"},
		{"mixedcase", "Acme-Bank"},
		{"alternating", "AcMe-BaNk"},
		{"lowercase", "acme-bank"},
	}

	for _, tc := range testCases {
		t.Run("GetBySlug_"+tc.name, func(t *testing.T) {
			retrieved, err := repo.GetBySlug(ctx, tc.slugCase)
			require.NoError(t, err, "GetBySlug(%q) should find tenant", tc.slugCase)
			assert.Equal(t, testTenant.ID.String(), retrieved.ID.String())
			assert.Equal(t, "acme-bank", retrieved.Slug)
			assert.Equal(t, "ACME Bank", retrieved.DisplayName)
		})

		t.Run("IsSlugAvailable_"+tc.name, func(t *testing.T) {
			available, err := repo.IsSlugAvailable(ctx, tc.slugCase)
			require.NoError(t, err, "IsSlugAvailable(%q) should succeed", tc.slugCase)
			assert.False(t, available, "IsSlugAvailable(%q) should return false (slug taken)", tc.slugCase)
		})
	}
}

// TestRepository_GetBySlug_UsesIndex verifies that slug lookups use the idx_tenant_slug
// index for O(log n) performance rather than O(n) sequential scan.
//
// PostgreSQL query planner behavior:
// - With < ~100 rows: May prefer Seq Scan due to overhead of index access
// - With sufficient rows: Uses Index Scan on idx_tenant_slug
// - The partial index (WHERE slug IS NOT NULL) is optimal for slug lookups
//
// This test creates enough rows to trigger index usage and verifies via EXPLAIN.
func TestRepository_GetBySlug_UsesIndex(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	ctx := context.Background()

	// Create partial index matching the migration
	// GORM AutoMigrate creates a basic unique index, but we need the partial index
	// that matches production behavior for accurate query plan testing
	err := db.Exec(`
		DROP INDEX IF EXISTS idx_tenant_slug;
		CREATE UNIQUE INDEX idx_tenant_slug ON tenant(slug) WHERE slug IS NOT NULL;
	`).Error
	require.NoError(t, err, "Failed to create partial index")

	// Insert enough tenants to make index usage preferable
	// PostgreSQL typically prefers indexes over sequential scans when:
	// 1. Table has many rows (reduces full scan cost)
	// 2. Query selectivity is high (few rows match)
	const numTenants = 200
	for i := 0; i < numTenants; i++ {
		testTenant := newTestTenant(fmt.Sprintf("tenant_%03d", i))
		testTenant.Slug = fmt.Sprintf("slug-%03d", i)
		err := db.WithContext(ctx).Create(toEntity(testTenant)).Error
		require.NoError(t, err, "Failed to create tenant %d", i)
	}

	// Run EXPLAIN on the GetBySlug query pattern
	var explainResult []struct {
		QueryPlan string `gorm:"column:QUERY PLAN"`
	}
	err = db.Raw("EXPLAIN SELECT * FROM tenant WHERE slug = ?", "slug-100").Scan(&explainResult).Error
	require.NoError(t, err, "EXPLAIN query failed")

	// Combine query plan lines for analysis
	var queryPlan string
	for _, row := range explainResult {
		queryPlan += row.QueryPlan + "\n"
	}

	// Verify index is used (not a sequential scan)
	// PostgreSQL will show "Index Scan using idx_tenant_slug" or similar
	assert.Contains(t, queryPlan, "Index",
		"Expected query plan to use Index Scan, got:\n%s", queryPlan)
	assert.NotContains(t, queryPlan, "Seq Scan",
		"Query plan should not use Seq Scan, got:\n%s", queryPlan)

	t.Logf("Query plan for GetBySlug:\n%s", queryPlan)
}

func TestRepository_ListByStatusOlderThan(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create tenants with different statuses and timestamps
	now := time.Now()
	twoHoursAgo := now.Add(-2 * time.Hour)
	thirtyMinutesAgo := now.Add(-30 * time.Minute)

	// Old failed tenant (should be included)
	oldFailed := newTestTenant("old_failed")
	oldFailed.Status = domain.StatusProvisioningFailed
	oldFailed.CreatedAt = twoHoursAgo
	oldFailed.ErrorMessage = "database connection timeout"
	err := db.WithContext(ctx).Create(toEntity(oldFailed)).Error
	require.NoError(t, err)
	// Set updated_at to match created_at for test purposes
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", oldFailed.ID.String()).Error
	require.NoError(t, err)

	// Recent failed tenant (should be excluded)
	recentFailed := newTestTenant("recent_failed")
	recentFailed.Status = domain.StatusProvisioningFailed
	recentFailed.CreatedAt = thirtyMinutesAgo
	recentFailed.ErrorMessage = "network error"
	err = db.WithContext(ctx).Create(toEntity(recentFailed)).Error
	require.NoError(t, err)
	// Set updated_at to match created_at for test purposes
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", recentFailed.ID.String()).Error
	require.NoError(t, err)

	// Old active tenant (should be excluded - different status)
	oldActive := newTestTenant("old_active")
	oldActive.Status = domain.StatusActive
	oldActive.CreatedAt = twoHoursAgo
	err = db.WithContext(ctx).Create(toEntity(oldActive)).Error
	require.NoError(t, err)
	// Set updated_at to match created_at for test purposes
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", oldActive.ID.String()).Error
	require.NoError(t, err)

	// Query for failed tenants older than 1 hour
	cutoff := now.Add(-1 * time.Hour)
	tenants, err := repo.ListByStatusOlderThan(ctx, domain.StatusProvisioningFailed, cutoff)
	require.NoError(t, err)

	// Should only return the old failed tenant
	assert.Len(t, tenants, 1)
	assert.Equal(t, "old_failed", tenants[0].ID.String())
	assert.Equal(t, domain.StatusProvisioningFailed, tenants[0].Status)
	assert.Equal(t, "database connection timeout", tenants[0].ErrorMessage)
	assert.True(t, tenants[0].CreatedAt.Before(cutoff))
}

func TestRepository_ListByStatusOlderThan_EmptyResult(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create only recent failed tenants
	now := time.Now()
	recentFailed := newTestTenant("recent_failed")
	recentFailed.Status = domain.StatusProvisioningFailed
	recentFailed.CreatedAt = now.Add(-10 * time.Minute)
	err := repo.Create(ctx, recentFailed)
	require.NoError(t, err)

	// Query with cutoff 1 hour ago (no tenants should match)
	cutoff := now.Add(-1 * time.Hour)
	tenants, err := repo.ListByStatusOlderThan(ctx, domain.StatusProvisioningFailed, cutoff)
	require.NoError(t, err)
	assert.Len(t, tenants, 0)
}

func TestRepository_ListByStatusOlderThan_BoundaryCondition(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	now := time.Now()
	exactlyOneHourAgo := now.Add(-1 * time.Hour)

	// Tenant created exactly 1 hour ago
	exactTenant := newTestTenant("exact_tenant")
	exactTenant.Status = domain.StatusProvisioningFailed
	exactTenant.CreatedAt = exactlyOneHourAgo
	err := db.WithContext(ctx).Create(toEntity(exactTenant)).Error
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", exactTenant.ID.String()).Error
	require.NoError(t, err)

	// Tenant created 1 hour + 1 second ago (should be included)
	olderTenant := newTestTenant("older_tenant")
	olderTenant.Status = domain.StatusProvisioningFailed
	olderTenant.CreatedAt = exactlyOneHourAgo.Add(-1 * time.Second)
	err = db.WithContext(ctx).Create(toEntity(olderTenant)).Error
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", olderTenant.ID.String()).Error
	require.NoError(t, err)

	// Tenant created 1 hour - 1 second ago (should be excluded)
	newerTenant := newTestTenant("newer_tenant")
	newerTenant.Status = domain.StatusProvisioningFailed
	newerTenant.CreatedAt = exactlyOneHourAgo.Add(1 * time.Second)
	err = db.WithContext(ctx).Create(toEntity(newerTenant)).Error
	require.NoError(t, err)
	err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", newerTenant.ID.String()).Error
	require.NoError(t, err)

	// Query with cutoff exactly 1 hour ago
	// created_at < cutoff means exact match is excluded
	tenants, err := repo.ListByStatusOlderThan(ctx, domain.StatusProvisioningFailed, exactlyOneHourAgo)
	require.NoError(t, err)

	// Should only return the tenant that is strictly older (1h + 1s ago)
	assert.Len(t, tenants, 1)
	assert.Equal(t, "older_tenant", tenants[0].ID.String())
}

func TestRepository_ListByStatusOlderThan_MultipleStatuses(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	now := time.Now()
	twoHoursAgo := now.Add(-2 * time.Hour)

	// Create old tenants with different statuses
	statuses := []domain.Status{
		domain.StatusProvisioningFailed,
		domain.StatusActive,
		domain.StatusSuspended,
		domain.StatusDeprovisioned,
	}

	for i, status := range statuses {
		tenant := newTestTenant(fmt.Sprintf("tenant_%d", i))
		tenant.Status = status
		tenant.CreatedAt = twoHoursAgo
		err := db.WithContext(ctx).Create(toEntity(tenant)).Error
		require.NoError(t, err)
		err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", tenant.ID.String()).Error
		require.NoError(t, err)
	}

	// Query for only failed tenants
	cutoff := now.Add(-1 * time.Hour)
	tenants, err := repo.ListByStatusOlderThan(ctx, domain.StatusProvisioningFailed, cutoff)
	require.NoError(t, err)

	// Should only return the provisioning_failed tenant
	assert.Len(t, tenants, 1)
	assert.Equal(t, domain.StatusProvisioningFailed, tenants[0].Status)
}

func TestRepository_ListByStatusOlderThan_OrderedByCreatedAt(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	now := time.Now()

	// Create multiple old failed tenants in non-chronological order
	timestamps := []time.Time{
		now.Add(-3 * time.Hour),
		now.Add(-5 * time.Hour),
		now.Add(-2 * time.Hour),
		now.Add(-4 * time.Hour),
	}

	for i, timestamp := range timestamps {
		tenant := newTestTenant(fmt.Sprintf("tenant_%d", i))
		tenant.Status = domain.StatusProvisioningFailed
		tenant.CreatedAt = timestamp
		err := db.WithContext(ctx).Create(toEntity(tenant)).Error
		require.NoError(t, err)
		err = db.Exec("UPDATE tenant SET updated_at = created_at WHERE id = ?", tenant.ID.String()).Error
		require.NoError(t, err)
	}

	// Query all failed tenants
	cutoff := now.Add(-1 * time.Hour)
	tenants, err := repo.ListByStatusOlderThan(ctx, domain.StatusProvisioningFailed, cutoff)
	require.NoError(t, err)

	// Verify results are ordered by updated_at ASC (oldest first)
	assert.Len(t, tenants, 4)
	assert.Equal(t, "tenant_1", tenants[0].ID.String()) // 5 hours ago (oldest)
	assert.Equal(t, "tenant_3", tenants[1].ID.String()) // 4 hours ago
	assert.Equal(t, "tenant_0", tenants[2].ID.String()) // 3 hours ago
	assert.Equal(t, "tenant_2", tenants[3].ID.String()) // 2 hours ago (newest)
}

func TestRepository_ConnBypassesTenantGuard(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Register TenantGuard — same as bootstrap.NewDatabase does in production.
	require.NoError(t, db.Use(dbpkg.NewTenantGuard()))

	repo := NewRepository(db)
	ctx := context.Background()

	// Without bypass, IsSlugAvailable would fail with ErrTenantScopeRequired.
	// The conn() helper applies WithTenantGuardBypass, so this should succeed.
	available, err := repo.IsSlugAvailable(ctx, "any-slug")
	require.NoError(t, err)
	assert.True(t, available)
}

func TestRepository_ConnBypassesTenantGuard_Create(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	require.NoError(t, db.Use(dbpkg.NewTenantGuard()))

	repo := NewRepository(db)
	ctx := context.Background()
	tenant := newTestTenant("guard_test")

	// Create should succeed despite TenantGuard being active.
	err := repo.Create(ctx, tenant)
	require.NoError(t, err)

	// GetByID should also succeed.
	retrieved, err := repo.GetByID(ctx, tenant.ID)
	require.NoError(t, err)
	assert.Equal(t, "guard_test", retrieved.ID.String())
}
