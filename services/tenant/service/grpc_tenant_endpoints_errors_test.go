package service

import (
	"context"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gorm.io/gorm"
)

// closeDB closes the underlying *sql.DB connection pool so subsequent repository
// queries fail with a generic (non-sentinel) driver error. The service maps these
// to codes.Internal, exercising the infrastructure-failure branches that are
// otherwise unreachable with a healthy database.
func closeDB(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
}

func requireStatusCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "error is not a gRPC status: %v", err)
	assert.Equal(t, want, st.Code(), "unexpected gRPC code for error: %v", err)
}

// TestService_InitiateTenant_SlugCheckDBError covers validateSlugAvailability's
// Internal branch when IsSlugAvailable fails with a generic DB error.
func TestService_InitiateTenant_SlugCheckDBError(t *testing.T) {
	svc, db, cleanup := setupTest(t)
	defer cleanup()

	closeDB(t, db)

	_, err := svc.InitiateTenant(context.Background(), &pb.InitiateTenantRequest{
		TenantId:        "slug_db_error",
		DisplayName:     "Slug DB Error",
		SettlementAsset: "GBP",
		Slug:            "valid-slug",
	})
	requireStatusCode(t, err, codes.Internal)
}

// TestService_InitiateTenant_PersistDBError covers persistNewTenant's Internal
// branch when repo.Create fails with a generic (non-sentinel) DB error.
func TestService_InitiateTenant_PersistDBError(t *testing.T) {
	svc, db, cleanup := setupTest(t)
	defer cleanup()

	closeDB(t, db)

	// No slug, so validateSlugAvailability short-circuits and Create is reached.
	_, err := svc.InitiateTenant(context.Background(), &pb.InitiateTenantRequest{
		TenantId:        "persist_db_error",
		DisplayName:     "Persist DB Error",
		SettlementAsset: "GBP",
	})
	requireStatusCode(t, err, codes.Internal)
}

// TestService_RetrieveTenant_DBError covers RetrieveTenant's Internal branch when
// repo.GetByID fails with a generic DB error (distinct from ErrTenantNotFound).
func TestService_RetrieveTenant_DBError(t *testing.T) {
	svc, db, cleanup := setupTest(t)
	defer cleanup()

	closeDB(t, db)

	_, err := svc.RetrieveTenant(context.Background(), &pb.RetrieveTenantRequest{
		TenantId: "retrieve_db_error",
	})
	requireStatusCode(t, err, codes.Internal)
}

// TestService_UpdateTenantStatus_LoadDBError covers validateStatusTransition's
// Internal branch when the initial GetByID fails with a generic DB error.
func TestService_UpdateTenantStatus_LoadDBError(t *testing.T) {
	svc, db, cleanup := setupTest(t)
	defer cleanup()

	closeDB(t, db)

	_, err := svc.UpdateTenantStatus(context.Background(), &pb.UpdateTenantStatusRequest{
		TenantId: "update_db_error",
		Status:   pb.TenantStatus_TENANT_STATUS_SUSPENDED,
	})
	requireStatusCode(t, err, codes.Internal)
}

// TestService_ListTenants_DBError covers ListTenants' Internal branch when
// repo.List fails with a generic DB error (admin/no-claims path).
func TestService_ListTenants_DBError(t *testing.T) {
	svc, db, cleanup := setupTest(t)
	defer cleanup()

	closeDB(t, db)

	// No claims in context -> admin/full-list path, reaches repo.List.
	_, err := svc.ListTenants(context.Background(), &pb.ListTenantsRequest{PageSize: 10})
	requireStatusCode(t, err, codes.Internal)
}

// TestService_ListTenants_NonAdmin_TenantNotFound covers listTenantsForNonAdmin's
// ErrTenantNotFound branch: a non-admin user whose tenant claim points to a tenant
// that does not exist returns an empty list (not an error).
func TestService_ListTenants_NonAdmin_TenantNotFound(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := ctxWithClaims(&auth.Claims{
		UserID:   "ghost-user",
		TenantID: "nonexistent_tenant",
		Roles:    []string{auth.RoleOperator.String()},
	})

	resp, err := svc.ListTenants(ctx, &pb.ListTenantsRequest{PageSize: 10})
	require.NoError(t, err)
	assert.Empty(t, resp.Tenants)
}

// TestService_ListTenants_NonAdmin_DBError covers listTenantsForNonAdmin's generic
// DB-error branch: a non-admin lookup that fails with a non-sentinel error still
// degrades to an empty list rather than surfacing an error.
func TestService_ListTenants_NonAdmin_DBError(t *testing.T) {
	svc, db, cleanup := setupTest(t)
	defer cleanup()

	closeDB(t, db)

	ctx := ctxWithClaims(&auth.Claims{
		UserID:   "tenant-user",
		TenantID: "some_tenant",
		Roles:    []string{auth.RoleOperator.String()},
	})

	resp, err := svc.ListTenants(ctx, &pb.ListTenantsRequest{PageSize: 10})
	require.NoError(t, err)
	assert.Empty(t, resp.Tenants)
}

// TestService_UpdateTenantStatus_VersionConflict covers executeStatusUpdate's
// ErrVersionConflict branch (codes.Aborted). It simulates the lost-update race by
// invoking executeStatusUpdate directly with a stale current.Version after the DB
// row's version has been bumped by an intervening update.
func TestService_UpdateTenantStatus_VersionConflict(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	_, err := svc.InitiateTenant(ctx, &pb.InitiateTenantRequest{
		TenantId:        "version_conflict",
		DisplayName:     "Version Conflict",
		SettlementAsset: "GBP",
	})
	require.NoError(t, err)

	tenantID, err := tenant.NewTenantID("version_conflict")
	require.NoError(t, err)

	// Load the current tenant (version 1) - this is the stale snapshot.
	current, err := svc.repo.GetByID(ctx, tenantID)
	require.NoError(t, err)
	require.Equal(t, 1, current.Version)

	// Intervening update bumps the row to version 2.
	_, err = svc.repo.UpdateStatus(ctx, tenantID, domain.StatusSuspended, current.Version)
	require.NoError(t, err)

	// executeStatusUpdate with the stale version-1 snapshot must hit the optimistic
	// lock and surface codes.Aborted.
	req := &pb.UpdateTenantStatusRequest{
		TenantId: "version_conflict",
		Status:   pb.TenantStatus_TENANT_STATUS_DEPROVISIONED,
	}
	_, err = svc.executeStatusUpdate(ctx, req, tenantID, domain.StatusDeprovisioned, current)
	requireStatusCode(t, err, codes.Aborted)
}

// TestService_GetBySlug_CacheHit_DBError covers lookupSlugInCache's branch where a
// cache hit resolves to a tenant ID, but the GetByID fails with a generic DB error
// (not ErrTenantNotFound). The lookup falls through; the subsequent DB GetBySlug
// also fails on the closed connection, so GetBySlug returns the error.
func TestService_GetBySlug_CacheHit_DBError(t *testing.T) {
	svc, _, mr, cleanup := setupServiceWithCache(t)
	defer cleanup()

	ctx := context.Background()

	// Create a tenant with a slug so the cache is pre-populated.
	_, err := svc.InitiateTenant(ctx, &pb.InitiateTenantRequest{
		TenantId:        "cache_db_error",
		DisplayName:     "Cache DB Error",
		SettlementAsset: "GBP",
		Slug:            "cache-db-error",
	})
	require.NoError(t, err)
	require.True(t, mr.Exists("tenant:slug:cache-db-error"))

	// Close the DB so the cache-hit GetByID fails with a generic error.
	closeDB(t, svc.repo.DB())

	_, err = svc.GetBySlug(ctx, "cache-db-error")
	require.Error(t, err)
}
