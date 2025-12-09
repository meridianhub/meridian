package persistence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/organization/domain"
	"github.com/meridianhub/meridian/shared/platform/organization"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) (*gorm.DB, func()) {
	t.Helper()
	return testdb.SetupPostgres(t, []interface{}{&OrganizationEntity{}})
}

func newTestOrganization(id string) *domain.Organization {
	orgID, _ := organization.NewOrganizationID(id)
	return &domain.Organization{
		ID:              orgID,
		DisplayName:     "Test Organization " + id,
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
	org := newTestOrganization("acme_bank")

	err := repo.Create(ctx, org)
	require.NoError(t, err)

	// Verify organization was saved
	retrieved, err := repo.GetByID(ctx, org.ID)
	require.NoError(t, err)
	assert.Equal(t, org.ID.String(), retrieved.ID.String())
	assert.Equal(t, org.DisplayName, retrieved.DisplayName)
	assert.Equal(t, org.SettlementAsset, retrieved.SettlementAsset)
	assert.Equal(t, domain.StatusActive, retrieved.Status)
	assert.Equal(t, 1, retrieved.Version)
}

func TestRepository_Create_WithSubdomain(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()
	org := newTestOrganization("acme_bank")
	org.Subdomain = "acme-bank.demo.meridian.io"

	err := repo.Create(ctx, org)
	require.NoError(t, err)

	// Verify subdomain was saved
	retrieved, err := repo.GetByID(ctx, org.ID)
	require.NoError(t, err)
	assert.Equal(t, "acme-bank.demo.meridian.io", retrieved.Subdomain)
}

func TestRepository_Create_DuplicateOrganization(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create first organization
	org1 := newTestOrganization("duplicate_org")
	err := repo.Create(ctx, org1)
	require.NoError(t, err)

	// Try to create duplicate
	org2 := newTestOrganization("duplicate_org")
	err = repo.Create(ctx, org2)
	assert.True(t, errors.Is(err, ErrOrganizationExists), "Expected ErrOrganizationExists, got %v", err)
}

func TestRepository_Create_DuplicateSubdomain(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create first organization with subdomain
	org1 := newTestOrganization("org_one")
	org1.Subdomain = "shared-subdomain.demo.meridian.io"
	err := repo.Create(ctx, org1)
	require.NoError(t, err)

	// Create second organization with same subdomain
	org2 := newTestOrganization("org_two")
	org2.Subdomain = "shared-subdomain.demo.meridian.io"
	err = repo.Create(ctx, org2)
	assert.True(t, errors.Is(err, ErrSubdomainTaken), "Expected ErrSubdomainTaken, got %v", err)
}

func TestRepository_GetByID_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	orgID, _ := organization.NewOrganizationID("nonexistent_org")
	_, err := repo.GetByID(ctx, orgID)
	assert.True(t, errors.Is(err, ErrOrganizationNotFound), "Expected ErrOrganizationNotFound, got %v", err)
}

func TestRepository_IsActive(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create active organization
	org := newTestOrganization("active_org")
	err := repo.Create(ctx, org)
	require.NoError(t, err)

	// Check IsActive returns true
	active, err := repo.IsActive(ctx, org.ID)
	require.NoError(t, err)
	assert.True(t, active)
}

func TestRepository_IsActive_Suspended(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create and suspend organization
	org := newTestOrganization("suspended_org")
	err := repo.Create(ctx, org)
	require.NoError(t, err)

	_, err = repo.UpdateStatus(ctx, org.ID, domain.StatusSuspended, org.Version)
	require.NoError(t, err)

	// Check IsActive returns false
	active, err := repo.IsActive(ctx, org.ID)
	require.NoError(t, err)
	assert.False(t, active)
}

func TestRepository_IsActive_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	orgID, _ := organization.NewOrganizationID("nonexistent_org")
	_, err := repo.IsActive(ctx, orgID)
	assert.True(t, errors.Is(err, ErrOrganizationNotFound), "Expected ErrOrganizationNotFound, got %v", err)
}

func TestRepository_UpdateStatus(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create organization
	org := newTestOrganization("status_test_org")
	err := repo.Create(ctx, org)
	require.NoError(t, err)

	// Update status to suspended
	updated, err := repo.UpdateStatus(ctx, org.ID, domain.StatusSuspended, 1)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusSuspended, updated.Status)
	assert.Equal(t, 2, updated.Version)
}

func TestRepository_UpdateStatus_Deprovisioned(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create organization
	org := newTestOrganization("deprov_test_org")
	err := repo.Create(ctx, org)
	require.NoError(t, err)

	// Update status to deprovisioned
	updated, err := repo.UpdateStatus(ctx, org.ID, domain.StatusDeprovisioned, 1)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusDeprovisioned, updated.Status)
	assert.NotNil(t, updated.DeprovisionedAt)
}

func TestRepository_UpdateStatus_VersionConflict(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create organization
	org := newTestOrganization("conflict_test_org")
	err := repo.Create(ctx, org)
	require.NoError(t, err)

	// Update with wrong version
	_, err = repo.UpdateStatus(ctx, org.ID, domain.StatusSuspended, 999)
	assert.True(t, errors.Is(err, ErrVersionConflict), "Expected ErrVersionConflict, got %v", err)
}

func TestRepository_UpdateStatus_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	orgID, _ := organization.NewOrganizationID("nonexistent_org")
	_, err := repo.UpdateStatus(ctx, orgID, domain.StatusSuspended, 1)
	assert.True(t, errors.Is(err, ErrOrganizationNotFound), "Expected ErrOrganizationNotFound, got %v", err)
}

func TestRepository_List(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create multiple organizations
	for i := 1; i <= 5; i++ {
		org := newTestOrganization(string(rune('a'+i)) + "_org")
		err := repo.Create(ctx, org)
		require.NoError(t, err)
		// Small delay to ensure distinct created_at timestamps
		time.Sleep(10 * time.Millisecond)
	}

	// List all organizations
	orgs, nextToken, err := repo.List(ctx, nil, 10, "")
	require.NoError(t, err)
	assert.Len(t, orgs, 5)
	assert.Empty(t, nextToken)
}

func TestRepository_List_WithStatusFilter(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create 3 active and 2 suspended organizations
	for i := 1; i <= 3; i++ {
		org := newTestOrganization("active_" + string(rune('a'+i)))
		err := repo.Create(ctx, org)
		require.NoError(t, err)
	}

	for i := 1; i <= 2; i++ {
		org := newTestOrganization("suspended_" + string(rune('a'+i)))
		org.Status = domain.StatusSuspended
		err := repo.Create(ctx, org)
		require.NoError(t, err)
	}

	// List only active organizations
	activeStatus := domain.StatusActive
	orgs, _, err := repo.List(ctx, &activeStatus, 10, "")
	require.NoError(t, err)
	assert.Len(t, orgs, 3)

	for _, org := range orgs {
		assert.Equal(t, domain.StatusActive, org.Status)
	}
}

func TestRepository_List_Pagination(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create 10 organizations
	for i := 0; i < 10; i++ {
		org := newTestOrganization("page_org_" + string(rune('a'+i)))
		err := repo.Create(ctx, org)
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
	for _, p1Org := range page1 {
		for _, p2Org := range page2 {
			assert.NotEqual(t, p1Org.ID, p2Org.ID, "Pages should not overlap")
		}
	}
}

func TestRepository_GetAll(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	repo := NewRepository(db)
	ctx := context.Background()

	// Create 5 organizations
	for i := 1; i <= 5; i++ {
		org := newTestOrganization("getall_" + string(rune('a'+i)))
		err := repo.Create(ctx, org)
		require.NoError(t, err)
	}

	// Get all
	orgs, err := repo.GetAll(ctx)
	require.NoError(t, err)
	assert.Len(t, orgs, 5)
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

	org := newTestOrganization("metadata_org")
	org.Metadata = map[string]interface{}{
		"tier":         "enterprise",
		"max_accounts": float64(10000),
		"features":     []interface{}{"multi-currency", "batch-payments"},
	}

	err := repo.Create(ctx, org)
	require.NoError(t, err)

	// Verify metadata was saved
	retrieved, err := repo.GetByID(ctx, org.ID)
	require.NoError(t, err)
	assert.Equal(t, "enterprise", retrieved.Metadata["tier"])
	assert.Equal(t, float64(10000), retrieved.Metadata["max_accounts"])
}
