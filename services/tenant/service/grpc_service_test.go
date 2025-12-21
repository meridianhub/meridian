package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"testing"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"gorm.io/gorm"
)

// createAuditOutboxTable creates the audit_outbox table required for GORM hooks on TenantEntity.
// This is needed in tests because the table is created by migration, not GORM AutoMigrate.
func createAuditOutboxTable(t *testing.T, db *gorm.DB) {
	t.Helper()
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
}

func setupTest(t *testing.T) (*Service, *gorm.DB, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
	createAuditOutboxTable(t, db)
	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	// Pass nil for provisioner and partyClient - skipped in basic tests
	svc := NewService(repo, nil, nil, logger)
	return svc, db, cleanup
}

func TestService_InitiateTenant(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateTenantRequest{
		TenantId:        "test_tenant",
		DisplayName:     "Test Tenant",
		SettlementAsset: "GBP",
	}

	resp, err := svc.InitiateTenant(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Tenant)

	assert.Equal(t, "test_tenant", resp.Tenant.TenantId)
	assert.Equal(t, "Test Tenant", resp.Tenant.DisplayName)
	assert.Equal(t, "GBP", resp.Tenant.SettlementAsset)
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_ACTIVE, resp.Tenant.Status)
	assert.Equal(t, int32(1), resp.Tenant.Version)
	assert.NotNil(t, resp.Tenant.CreatedAt)
}

func TestService_InitiateTenant_WithSubdomain(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateTenantRequest{
		TenantId:        "acme_bank",
		DisplayName:     "Acme Bank",
		SettlementAsset: "USD",
		Subdomain:       "acme-bank.demo.meridian.io",
	}

	resp, err := svc.InitiateTenant(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "acme-bank.demo.meridian.io", resp.Tenant.Subdomain)
}

func TestService_InitiateTenant_WithMetadata(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	metadata, _ := structpb.NewStruct(map[string]interface{}{
		"tier":     "enterprise",
		"features": []interface{}{"multi-currency"},
	})

	req := &pb.InitiateTenantRequest{
		TenantId:        "enterprise_tenant",
		DisplayName:     "Enterprise Tenant",
		SettlementAsset: "GBP",
		Metadata:        metadata,
	}

	resp, err := svc.InitiateTenant(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Tenant.Metadata)
	assert.Equal(t, "enterprise", resp.Tenant.Metadata.Fields["tier"].GetStringValue())
}

func TestService_InitiateTenant_InvalidID(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateTenantRequest{
		TenantId:        "invalid-id-with-dashes", // Dashes not allowed
		DisplayName:     "Invalid Tenant",
		SettlementAsset: "GBP",
	}

	_, err := svc.InitiateTenant(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestService_InitiateTenant_Duplicate(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateTenantRequest{
		TenantId:        "duplicate_tenant",
		DisplayName:     "First Tenant",
		SettlementAsset: "GBP",
	}

	// Create first tenant
	_, err := svc.InitiateTenant(ctx, req)
	require.NoError(t, err)

	// Try to create duplicate
	req.DisplayName = "Second Tenant"
	_, err = svc.InitiateTenant(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.AlreadyExists, st.Code())
}

func TestService_RetrieveTenant(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create tenant
	createReq := &pb.InitiateTenantRequest{
		TenantId:        "retrieve_test",
		DisplayName:     "Retrieve Test",
		SettlementAsset: "GBP",
	}
	_, err := svc.InitiateTenant(ctx, createReq)
	require.NoError(t, err)

	// Retrieve tenant
	retrieveReq := &pb.RetrieveTenantRequest{
		TenantId: "retrieve_test",
	}
	resp, err := svc.RetrieveTenant(ctx, retrieveReq)
	require.NoError(t, err)
	assert.Equal(t, "retrieve_test", resp.Tenant.TenantId)
	assert.Equal(t, "Retrieve Test", resp.Tenant.DisplayName)
}

func TestService_RetrieveTenant_NotFound(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.RetrieveTenantRequest{
		TenantId: "nonexistent",
	}

	_, err := svc.RetrieveTenant(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestService_UpdateTenantStatus(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create tenant
	createReq := &pb.InitiateTenantRequest{
		TenantId:        "status_test",
		DisplayName:     "Status Test",
		SettlementAsset: "GBP",
	}
	_, err := svc.InitiateTenant(ctx, createReq)
	require.NoError(t, err)

	// Update status to suspended
	updateReq := &pb.UpdateTenantStatusRequest{
		TenantId: "status_test",
		Status:   pb.TenantStatus_TENANT_STATUS_SUSPENDED,
	}
	resp, err := svc.UpdateTenantStatus(ctx, updateReq)
	require.NoError(t, err)
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_SUSPENDED, resp.Tenant.Status)
	assert.Equal(t, int32(2), resp.Tenant.Version)
}

func TestService_UpdateTenantStatus_ToDeprovisioned(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create tenant
	createReq := &pb.InitiateTenantRequest{
		TenantId:        "deprov_test",
		DisplayName:     "Deprov Test",
		SettlementAsset: "GBP",
	}
	_, err := svc.InitiateTenant(ctx, createReq)
	require.NoError(t, err)

	// Update status to deprovisioned
	updateReq := &pb.UpdateTenantStatusRequest{
		TenantId: "deprov_test",
		Status:   pb.TenantStatus_TENANT_STATUS_DEPROVISIONED,
	}
	resp, err := svc.UpdateTenantStatus(ctx, updateReq)
	require.NoError(t, err)
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_DEPROVISIONED, resp.Tenant.Status)
	assert.NotNil(t, resp.Tenant.DeprovisionedAt)
}

func TestService_UpdateTenantStatus_NotFound(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.UpdateTenantStatusRequest{
		TenantId: "nonexistent",
		Status:   pb.TenantStatus_TENANT_STATUS_SUSPENDED,
	}

	_, err := svc.UpdateTenantStatus(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestService_UpdateTenantStatus_InvalidStatus(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create tenant
	createReq := &pb.InitiateTenantRequest{
		TenantId:        "invalid_status_test",
		DisplayName:     "Invalid Status Test",
		SettlementAsset: "GBP",
	}
	_, err := svc.InitiateTenant(ctx, createReq)
	require.NoError(t, err)

	// Try to update with unspecified status
	updateReq := &pb.UpdateTenantStatusRequest{
		TenantId: "invalid_status_test",
		Status:   pb.TenantStatus_TENANT_STATUS_UNSPECIFIED,
	}
	_, err = svc.UpdateTenantStatus(ctx, updateReq)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestService_ListTenants(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create 5 tenants
	for i := 0; i < 5; i++ {
		req := &pb.InitiateTenantRequest{
			TenantId:        "list_tenant_" + strconv.Itoa(i),
			DisplayName:     "List Tenant " + strconv.Itoa(i),
			SettlementAsset: "GBP",
		}
		_, err := svc.InitiateTenant(ctx, req)
		require.NoError(t, err)
	}

	// List all tenants
	listReq := &pb.ListTenantsRequest{
		PageSize: 10,
	}
	resp, err := svc.ListTenants(ctx, listReq)
	require.NoError(t, err)
	assert.Len(t, resp.Tenants, 5)
}

func TestService_ListTenants_WithStatusFilter(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create 3 active tenants
	for i := 0; i < 3; i++ {
		req := &pb.InitiateTenantRequest{
			TenantId:        "active_" + strconv.Itoa(i),
			DisplayName:     "Active " + strconv.Itoa(i),
			SettlementAsset: "GBP",
		}
		_, err := svc.InitiateTenant(ctx, req)
		require.NoError(t, err)
	}

	// Suspend 2 of them
	for i := 0; i < 2; i++ {
		updateReq := &pb.UpdateTenantStatusRequest{
			TenantId: "active_" + strconv.Itoa(i),
			Status:   pb.TenantStatus_TENANT_STATUS_SUSPENDED,
		}
		_, err := svc.UpdateTenantStatus(ctx, updateReq)
		require.NoError(t, err)
	}

	// List only active tenants
	listReq := &pb.ListTenantsRequest{
		StatusFilter: pb.TenantStatus_TENANT_STATUS_ACTIVE,
		PageSize:     10,
	}
	resp, err := svc.ListTenants(ctx, listReq)
	require.NoError(t, err)
	assert.Len(t, resp.Tenants, 1)
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_ACTIVE, resp.Tenants[0].Status)
}

func TestService_ListTenants_Pagination(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create 10 tenants
	for i := 0; i < 10; i++ {
		req := &pb.InitiateTenantRequest{
			TenantId:        "page_tenant_" + strconv.Itoa(i),
			DisplayName:     "Page Tenant " + strconv.Itoa(i),
			SettlementAsset: "GBP",
		}
		_, err := svc.InitiateTenant(ctx, req)
		require.NoError(t, err)
	}

	// Get first page of 3
	listReq := &pb.ListTenantsRequest{
		PageSize: 3,
	}
	resp, err := svc.ListTenants(ctx, listReq)
	require.NoError(t, err)
	assert.Len(t, resp.Tenants, 3)
	assert.NotEmpty(t, resp.NextPageToken)

	// Get second page
	listReq.PageToken = resp.NextPageToken
	resp2, err := svc.ListTenants(ctx, listReq)
	require.NoError(t, err)
	assert.Len(t, resp2.Tenants, 3)

	// Verify no overlap
	for _, tenant1 := range resp.Tenants {
		for _, tenant2 := range resp2.Tenants {
			assert.NotEqual(t, tenant1.TenantId, tenant2.TenantId, "Pages should not overlap")
		}
	}
}

// mockPartyClient is a test implementation of the PartyClient interface.
type mockPartyClient struct {
	registerPartyFunc func(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.Party, error)
}

func (m *mockPartyClient) RegisterParty(ctx context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.Party, error) {
	if m.registerPartyFunc != nil {
		return m.registerPartyFunc(ctx, req)
	}
	return nil, nil
}

func (m *mockPartyClient) Close() error {
	return nil
}

func setupTestWithPartyClient(t *testing.T, partyClient *mockPartyClient) (*Service, *gorm.DB, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
	createAuditOutboxTable(t, db)
	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := NewService(repo, nil, partyClient, logger)
	return svc, db, cleanup
}

func setupTestWithProvisioner(t *testing.T, mockProv *provisioner.MockProvisioner) (*Service, *gorm.DB, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
	createAuditOutboxTable(t, db)
	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := NewService(repo, mockProv, nil, logger)
	return svc, db, cleanup
}

func TestService_InitiateTenant_WithPartyRegistration(t *testing.T) {
	mockClient := &mockPartyClient{
		registerPartyFunc: func(_ context.Context, req *partyv1.RegisterPartyRequest) (*partyv1.Party, error) {
			// Verify the request contains expected data
			assert.Equal(t, partyv1.PartyType_PARTY_TYPE_ORGANIZATION, req.PartyType)
			assert.NotEmpty(t, req.LegalName)
			assert.Equal(t, "party_linked_tenant", req.ExternalReference) // Bidirectional link
			return &partyv1.Party{
				PartyId:           "party_123",
				PartyType:         partyv1.PartyType_PARTY_TYPE_ORGANIZATION,
				LegalName:         req.LegalName,
				ExternalReference: req.ExternalReference,
				Status:            partyv1.PartyStatus_PARTY_STATUS_ACTIVE,
			}, nil
		},
	}

	svc, _, cleanup := setupTestWithPartyClient(t, mockClient)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateTenantRequest{
		TenantId:        "party_linked_tenant",
		DisplayName:     "Party Linked Tenant",
		SettlementAsset: "USD",
	}

	resp, err := svc.InitiateTenant(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Tenant)

	assert.Equal(t, "party_linked_tenant", resp.Tenant.TenantId)
	assert.Equal(t, "party_123", resp.Tenant.PartyId)
}

// errPartyServiceUnavailable is a test error for simulating party service failures.
var errPartyServiceUnavailable = errors.New("party service unavailable")

func TestService_InitiateTenant_PartyRegistrationFailure(t *testing.T) {
	mockClient := &mockPartyClient{
		registerPartyFunc: func(_ context.Context, _ *partyv1.RegisterPartyRequest) (*partyv1.Party, error) {
			return nil, errPartyServiceUnavailable
		},
	}

	svc, _, cleanup := setupTestWithPartyClient(t, mockClient)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateTenantRequest{
		TenantId:        "party_fail_tenant",
		DisplayName:     "Party Fail Tenant",
		SettlementAsset: "GBP",
	}

	_, err := svc.InitiateTenant(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "failed to register party")
}

// Tests for schema provisioning integration

func TestService_InitiateTenant_WithProvisioningSuccess(t *testing.T) {
	// Create mock provisioner that succeeds
	mockProv := provisioner.NewMockProvisioner([]provisioner.ServiceConfig{
		{Name: "party", MigrationPath: "testdata/migrations"},
		{Name: "current-account", MigrationPath: "testdata/migrations"},
	})

	svc, _, cleanup := setupTestWithProvisioner(t, mockProv)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateTenantRequest{
		TenantId:        "provisioned_tenant",
		DisplayName:     "Provisioned Tenant",
		SettlementAsset: "GBP",
	}

	resp, err := svc.InitiateTenant(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Tenant)

	// Tenant should be active after successful provisioning
	assert.Equal(t, "provisioned_tenant", resp.Tenant.TenantId)
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_ACTIVE, resp.Tenant.Status)
	assert.Equal(t, int32(2), resp.Tenant.Version) // Created with version 1, updated to active (version 2)

	// Verify provisioner was called
	require.Len(t, mockProv.ProvisioningCalls, 1)
	assert.Equal(t, "provisioned_tenant", mockProv.ProvisioningCalls[0].String())
}

// errProvisioningFailed is a test error for simulating provisioning failures.
var errProvisioningFailed = errors.New("provisioning failed: database connection error")

func TestService_InitiateTenant_WithProvisioningFailure(t *testing.T) {
	// Create mock provisioner that fails
	mockProv := provisioner.NewMockProvisioner([]provisioner.ServiceConfig{
		{Name: "party", MigrationPath: "testdata/migrations"},
	})
	mockProv.FailProvisioningFor["prov_fail_tenant"] = errProvisioningFailed

	svc, db, cleanup := setupTestWithProvisioner(t, mockProv)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateTenantRequest{
		TenantId:        "prov_fail_tenant",
		DisplayName:     "Provisioning Fail Tenant",
		SettlementAsset: "GBP",
	}

	_, err := svc.InitiateTenant(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
	assert.Contains(t, st.Message(), "schema provisioning failed")

	// Verify the tenant was created with provisioning_failed status
	var entity persistence.TenantEntity
	result := db.Where("id = ?", "prov_fail_tenant").First(&entity)
	require.NoError(t, result.Error)
	assert.Equal(t, "provisioning_failed", entity.Status)
	require.NotNil(t, entity.ErrorMessage)
	assert.Contains(t, *entity.ErrorMessage, "provisioning failed")
}

func TestService_InitiateTenant_WithoutProvisioner(t *testing.T) {
	// Service without provisioner - tenant should be created as active directly
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateTenantRequest{
		TenantId:        "no_prov_tenant",
		DisplayName:     "No Provisioning Tenant",
		SettlementAsset: "USD",
	}

	resp, err := svc.InitiateTenant(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Tenant)

	// Should be active immediately (no provisioning)
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_ACTIVE, resp.Tenant.Status)
	assert.Equal(t, int32(1), resp.Tenant.Version) // Only one version (created)
}

// Tests for ReconcileMigrations authorization

func TestReconcileMigrations_Authorization(t *testing.T) {
	// Create a mock provisioner
	mockProvisioner := provisioner.NewMockProvisioner([]provisioner.ServiceConfig{
		{Name: "party", MigrationPath: "/migrations/party"},
	})

	// Create service with mock provisioner
	svc := NewService(nil, mockProvisioner, nil, slog.Default())

	tests := []struct {
		name         string
		claims       *auth.Claims
		expectError  bool
		expectedCode codes.Code
		expectedMsg  string
	}{
		{
			name: "platform-admin allowed",
			claims: &auth.Claims{
				UserID: "admin-123",
				Roles:  []string{auth.RolePlatformAdmin},
			},
			expectError: false,
		},
		{
			name: "super-admin allowed",
			claims: &auth.Claims{
				UserID: "admin-456",
				Roles:  []string{auth.RoleSuperAdmin},
			},
			expectError: false,
		},
		{
			name: "platform-admin with other roles allowed",
			claims: &auth.Claims{
				UserID: "admin-789",
				Roles:  []string{"user", auth.RolePlatformAdmin, "viewer"},
			},
			expectError: false,
		},
		{
			name: "operator denied",
			claims: &auth.Claims{
				UserID: "user-123",
				Roles:  []string{"operator"},
			},
			expectError:  true,
			expectedCode: codes.PermissionDenied,
			expectedMsg:  "platform-admin or super-admin role required",
		},
		{
			name: "tenant admin denied",
			claims: &auth.Claims{
				UserID: "user-456",
				Roles:  []string{"admin"}, // tenant-scoped admin, not platform-admin
			},
			expectError:  true,
			expectedCode: codes.PermissionDenied,
			expectedMsg:  "platform-admin or super-admin role required",
		},
		{
			name: "no roles denied",
			claims: &auth.Claims{
				UserID: "user-789",
				Roles:  []string{},
			},
			expectError:  true,
			expectedCode: codes.PermissionDenied,
			expectedMsg:  "platform-admin or super-admin role required",
		},
		{
			name: "user role denied",
			claims: &auth.Claims{
				UserID: "user-101",
				Roles:  []string{"user", "viewer", "auditor"},
			},
			expectError:  true,
			expectedCode: codes.PermissionDenied,
			expectedMsg:  "platform-admin or super-admin role required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create context with claims
			ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, tt.claims)

			// Execute
			resp, err := svc.ReconcileMigrations(ctx, &pb.ReconcileMigrationsRequest{})

			// Assert
			if tt.expectError {
				require.Error(t, err)
				st, ok := status.FromError(err)
				require.True(t, ok, "error should be a gRPC status")
				assert.Equal(t, tt.expectedCode, st.Code())
				assert.Contains(t, st.Message(), tt.expectedMsg)
				assert.Nil(t, resp)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, resp)
			}
		})
	}
}

func TestReconcileMigrations_MissingClaims(t *testing.T) {
	// Create a mock provisioner
	mockProvisioner := provisioner.NewMockProvisioner([]provisioner.ServiceConfig{
		{Name: "party", MigrationPath: "/migrations/party"},
	})

	// Create service with mock provisioner
	svc := NewService(nil, mockProvisioner, nil, slog.Default())

	// Context without claims
	ctx := context.Background()

	// Execute
	resp, err := svc.ReconcileMigrations(ctx, &pb.ReconcileMigrationsRequest{})

	// Assert
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "error should be a gRPC status")
	assert.Equal(t, codes.Unauthenticated, st.Code())
	assert.Contains(t, st.Message(), "authentication required")
	assert.Nil(t, resp)
}

func TestReconcileMigrations_NoProvisioner(t *testing.T) {
	// Create service without provisioner
	svc := NewService(nil, nil, nil, slog.Default())

	// Create context with valid claims
	claims := &auth.Claims{
		UserID: "admin-123",
		Roles:  []string{auth.RolePlatformAdmin},
	}
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, claims)

	// Execute
	resp, err := svc.ReconcileMigrations(ctx, &pb.ReconcileMigrationsRequest{})

	// Assert - should fail with FailedPrecondition because provisioner is nil
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "error should be a gRPC status")
	assert.Equal(t, codes.FailedPrecondition, st.Code())
	assert.Contains(t, st.Message(), "schema provisioning not enabled")
	assert.Nil(t, resp)
}

func TestReconcileMigrations_AuthorizationBeforeProvisioner(t *testing.T) {
	// Test that authorization check happens BEFORE provisioner check.
	// This is important: we want to reject unauthorized users before
	// revealing any details about system configuration.

	// Create service WITHOUT provisioner (nil)
	svc := NewService(nil, nil, nil, slog.Default())

	// Create context with unauthorized claims
	claims := &auth.Claims{
		UserID: "user-123",
		Roles:  []string{"user"},
	}
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, claims)

	// Execute
	resp, err := svc.ReconcileMigrations(ctx, &pb.ReconcileMigrationsRequest{})

	// Assert - should fail with PermissionDenied (not FailedPrecondition)
	// This proves authorization check happens before provisioner check
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok, "error should be a gRPC status")
	assert.Equal(t, codes.PermissionDenied, st.Code(), "authorization should be checked before provisioner availability")
	assert.Contains(t, st.Message(), "platform-admin or super-admin role required")
	assert.Nil(t, resp)
}

func TestReconcileMigrations_SuccessfulReconciliation(t *testing.T) {
	// Create a mock provisioner with some active tenants
	mockProvisioner := provisioner.NewMockProvisioner([]provisioner.ServiceConfig{
		{Name: "party", MigrationPath: "/migrations/party"},
		{Name: "current-account", MigrationPath: "/migrations/current-account"},
	})

	// Add some tenant statuses that would be reconciled
	mockProvisioner.SetStatus(&provisioner.ProvisioningStatus{
		TenantID: "acme_bank",
		State:    provisioner.StateActive,
	})
	mockProvisioner.SetStatus(&provisioner.ProvisioningStatus{
		TenantID: "beta_corp",
		State:    provisioner.StateActive,
	})

	// Create service with mock provisioner
	svc := NewService(nil, mockProvisioner, nil, slog.Default())

	// Create context with platform-admin claims
	claims := &auth.Claims{
		UserID: "admin-123",
		Roles:  []string{auth.RolePlatformAdmin},
	}
	ctx := context.WithValue(context.Background(), auth.ClaimsContextKey, claims)

	// Execute - reconcile all tenants
	resp, err := svc.ReconcileMigrations(ctx, &pb.ReconcileMigrationsRequest{})

	// Assert
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, int32(2), resp.ReconciledCount, "should have reconciled 2 active tenants")
	assert.Empty(t, resp.Errors, "should have no errors")
}

// Tests for status conversion functions

func TestService_toProtoStatus(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	tests := []struct {
		name          string
		domainStatus  domain.Status
		expectedProto pb.TenantStatus
	}{
		{
			name:          "provisioning_pending to proto",
			domainStatus:  domain.StatusProvisioningPending,
			expectedProto: pb.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING,
		},
		{
			name:          "provisioning to proto",
			domainStatus:  domain.StatusProvisioning,
			expectedProto: pb.TenantStatus_TENANT_STATUS_PROVISIONING,
		},
		{
			name:          "provisioning_failed to proto",
			domainStatus:  domain.StatusProvisioningFailed,
			expectedProto: pb.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED,
		},
		{
			name:          "active to proto",
			domainStatus:  domain.StatusActive,
			expectedProto: pb.TenantStatus_TENANT_STATUS_ACTIVE,
		},
		{
			name:          "suspended to proto",
			domainStatus:  domain.StatusSuspended,
			expectedProto: pb.TenantStatus_TENANT_STATUS_SUSPENDED,
		},
		{
			name:          "deprovisioned to proto",
			domainStatus:  domain.StatusDeprovisioned,
			expectedProto: pb.TenantStatus_TENANT_STATUS_DEPROVISIONED,
		},
		{
			name:          "unknown status to unspecified",
			domainStatus:  domain.Status("unknown"),
			expectedProto: pb.TenantStatus_TENANT_STATUS_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.toProtoStatus(tt.domainStatus)
			assert.Equal(t, tt.expectedProto, result)
		})
	}
}

func TestService_toDomainStatus(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	tests := []struct {
		name           string
		protoStatus    pb.TenantStatus
		expectedDomain domain.Status
		expectError    bool
	}{
		{
			name:           "proto provisioning_pending to domain",
			protoStatus:    pb.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING,
			expectedDomain: domain.StatusProvisioningPending,
			expectError:    false,
		},
		{
			name:           "proto provisioning to domain",
			protoStatus:    pb.TenantStatus_TENANT_STATUS_PROVISIONING,
			expectedDomain: domain.StatusProvisioning,
			expectError:    false,
		},
		{
			name:           "proto provisioning_failed to domain",
			protoStatus:    pb.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED,
			expectedDomain: domain.StatusProvisioningFailed,
			expectError:    false,
		},
		{
			name:           "proto active to domain",
			protoStatus:    pb.TenantStatus_TENANT_STATUS_ACTIVE,
			expectedDomain: domain.StatusActive,
			expectError:    false,
		},
		{
			name:           "proto suspended to domain",
			protoStatus:    pb.TenantStatus_TENANT_STATUS_SUSPENDED,
			expectedDomain: domain.StatusSuspended,
			expectError:    false,
		},
		{
			name:           "proto deprovisioned to domain",
			protoStatus:    pb.TenantStatus_TENANT_STATUS_DEPROVISIONED,
			expectedDomain: domain.StatusDeprovisioned,
			expectError:    false,
		},
		{
			name:           "proto unspecified returns error",
			protoStatus:    pb.TenantStatus_TENANT_STATUS_UNSPECIFIED,
			expectedDomain: "",
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := svc.toDomainStatus(tt.protoStatus)
			if tt.expectError {
				require.Error(t, err)
				assert.Equal(t, ErrUnknownStatus, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedDomain, result)
			}
		})
	}
}

func TestService_StatusConversionRoundtrip(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	// Test roundtrip: domain -> proto -> domain
	tests := []struct {
		name   string
		status domain.Status
	}{
		{name: "provisioning_pending roundtrip", status: domain.StatusProvisioningPending},
		{name: "provisioning roundtrip", status: domain.StatusProvisioning},
		{name: "provisioning_failed roundtrip", status: domain.StatusProvisioningFailed},
		{name: "active roundtrip", status: domain.StatusActive},
		{name: "suspended roundtrip", status: domain.StatusSuspended},
		{name: "deprovisioned roundtrip", status: domain.StatusDeprovisioned},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Convert to proto and back
			protoStatus := svc.toProtoStatus(tt.status)
			domainStatus, err := svc.toDomainStatus(protoStatus)

			require.NoError(t, err)
			assert.Equal(t, tt.status, domainStatus, "roundtrip conversion should preserve status")
		})
	}
}
