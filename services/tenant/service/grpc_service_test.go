package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	partyv1 "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/domain"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/tenant"
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
	// Create mock provisioner
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

	// Tenant should be created with provisioning_pending status (worker will handle provisioning)
	assert.Equal(t, "provisioned_tenant", resp.Tenant.TenantId)
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING, resp.Tenant.Status)
	assert.Equal(t, int32(1), resp.Tenant.Version) // Created with version 1, no status update

	// Verify provisioner was NOT called during InitiateTenant
	assert.Empty(t, mockProv.ProvisioningCalls, "ProvisionSchemas should not be called during InitiateTenant - worker handles provisioning asynchronously")
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

	// InitiateTenant should succeed with provisioning_pending status
	resp, err := svc.InitiateTenant(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Tenant)

	// Tenant should be created with provisioning_pending status
	// The worker will attempt provisioning and handle the failure
	assert.Equal(t, "prov_fail_tenant", resp.Tenant.TenantId)
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING, resp.Tenant.Status)

	// Verify the tenant was persisted with provisioning_pending status
	var entity persistence.TenantEntity
	result := db.Where("id = ?", "prov_fail_tenant").First(&entity)
	require.NoError(t, result.Error)
	assert.Equal(t, "provisioning_pending", entity.Status)
	assert.Nil(t, entity.ErrorMessage, "no error message yet - worker will handle provisioning failure")

	// Verify provisioner was NOT called during InitiateTenant
	assert.Empty(t, mockProv.ProvisioningCalls, "ProvisionSchemas should not be called during InitiateTenant")
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

// TestInitiateTenant_CreatesProvisioningStatusRecords verifies that InitiateTenant
// creates provisioning_status records when a provisioner is configured.
func TestInitiateTenant_CreatesProvisioningStatusRecords(t *testing.T) {
	// Create mock provisioner with test services
	mockProv := provisioner.NewMockProvisioner([]provisioner.ServiceConfig{
		{Name: "party", MigrationPath: "/migrations/party"},
		{Name: "current-account", MigrationPath: "/migrations/current-account"},
	})

	svc, _, cleanup := setupTestWithProvisioner(t, mockProv)
	defer cleanup()

	// Test: Create tenant with provisioner configured
	req := &pb.InitiateTenantRequest{
		TenantId:        "test_org_prov_status",
		DisplayName:     "Test Organization",
		SettlementAsset: "USD",
	}

	resp, err := svc.InitiateTenant(context.Background(), req)
	require.NoError(t, err, "InitiateTenant should succeed")
	require.NotNil(t, resp)

	// Verify tenant was created with provisioning_pending status
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_PROVISIONING_PENDING, resp.Tenant.Status)

	// Verify provisioning status record was created
	tenantID, err := tenant.NewTenantID("test_org_prov_status")
	require.NoError(t, err)

	status, err := mockProv.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err, "Provisioning status should exist")
	assert.Equal(t, provisioner.StatePending, status.State)
	assert.Len(t, status.Services, 2, "Should have 2 service status records")

	// Verify service schemas
	assert.Equal(t, "party", status.Services[0].ServiceName)
	assert.Equal(t, provisioner.ServiceStatePending, status.Services[0].State)
	assert.Equal(t, "current-account", status.Services[1].ServiceName)
	assert.Equal(t, provisioner.ServiceStatePending, status.Services[1].State)
}

// TestInitiateTenant_ProvisioningStatusIdempotent verifies that calling
// InitiateTenant multiple times doesn't duplicate provisioning status.
func TestInitiateTenant_ProvisioningStatusIdempotent(t *testing.T) {
	// Create mock provisioner
	mockProv := provisioner.NewMockProvisioner([]provisioner.ServiceConfig{
		{Name: "party", MigrationPath: "/migrations/party"},
	})

	svc, _, cleanup := setupTestWithProvisioner(t, mockProv)
	defer cleanup()

	// First call: Create tenant
	req := &pb.InitiateTenantRequest{
		TenantId:        "test_org_idempotent",
		DisplayName:     "Test Organization Idempotent",
		SettlementAsset: "USD",
	}

	resp, err := svc.InitiateTenant(context.Background(), req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify provisioning status was created
	tenantID, err := tenant.NewTenantID("test_org_idempotent")
	require.NoError(t, err)

	status1, err := mockProv.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, provisioner.StatePending, status1.State)

	// Second call: Try to create same tenant again (will fail at tenant creation level)
	// But if we manually call the helper, it should be idempotent
	err = svc.createProvisioningStatusRecords(context.Background(), tenantID)
	require.NoError(t, err, "Second call should be idempotent")

	// Verify status hasn't changed
	status2, err := mockProv.GetProvisioningStatus(context.Background(), tenantID)
	require.NoError(t, err)
	assert.Equal(t, status1.State, status2.State)
	assert.Equal(t, status1.CreatedAt, status2.CreatedAt, "Created timestamp should not change on second call")
}

// Tests for GetTenantProvisioningStatus

func TestService_GetTenantProvisioningStatus_Success(t *testing.T) {
	svc, db, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create tenant with provisioning status
	createReq := &pb.InitiateTenantRequest{
		TenantId:        "provisioning_status_test",
		DisplayName:     "Provisioning Status Test",
		SettlementAsset: "GBP",
	}
	_, err := svc.InitiateTenant(ctx, createReq)
	require.NoError(t, err)

	// Auto-migrate the provisioning status table
	err = db.AutoMigrate(&persistence.ProvisioningStatusEntity{})
	require.NoError(t, err)

	// Insert test provisioning status records directly via GORM
	partyStarted := time.Now().Add(-5 * time.Minute)
	partyCompleted := time.Now().Add(-3 * time.Minute)
	accountStarted := time.Now().Add(-2 * time.Minute)

	err = db.Create(&persistence.ProvisioningStatusEntity{
		TenantID:         "provisioning_status_test",
		ServiceName:      "party",
		Status:           string(domain.ServiceStatusCompleted),
		MigrationVersion: stringPtr("20240115_001"),
		StartedAt:        &partyStarted,
		CompletedAt:      &partyCompleted,
	}).Error
	require.NoError(t, err)

	err = db.Create(&persistence.ProvisioningStatusEntity{
		TenantID:         "provisioning_status_test",
		ServiceName:      "account",
		Status:           string(domain.ServiceStatusInProgress),
		MigrationVersion: stringPtr("20240120_002"),
		StartedAt:        &accountStarted,
	}).Error
	require.NoError(t, err)

	err = db.Create(&persistence.ProvisioningStatusEntity{
		TenantID:    "provisioning_status_test",
		ServiceName: "transaction",
		Status:      string(domain.ServiceStatusPending),
	}).Error
	require.NoError(t, err)

	// Get provisioning status
	req := &pb.GetTenantProvisioningStatusRequest{
		TenantId: "provisioning_status_test",
	}
	resp, err := svc.GetTenantProvisioningStatus(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify response structure
	assert.Equal(t, "provisioning_status_test", resp.TenantId)
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_ACTIVE, resp.OverallStatus)
	assert.Len(t, resp.Services, 3)

	// Verify service statuses are returned in alphabetical order
	assert.Equal(t, "account", resp.Services[0].ServiceName)
	assert.Equal(t, pb.ServiceProvisioningStatus_STATUS_IN_PROGRESS, resp.Services[0].Status)
	assert.Equal(t, "20240120_002", resp.Services[0].MigrationVersion)
	assert.NotNil(t, resp.Services[0].StartedAt)
	assert.Nil(t, resp.Services[0].CompletedAt)

	assert.Equal(t, "party", resp.Services[1].ServiceName)
	assert.Equal(t, pb.ServiceProvisioningStatus_STATUS_COMPLETED, resp.Services[1].Status)
	assert.Equal(t, "20240115_001", resp.Services[1].MigrationVersion)
	assert.NotNil(t, resp.Services[1].StartedAt)
	assert.NotNil(t, resp.Services[1].CompletedAt)

	assert.Equal(t, "transaction", resp.Services[2].ServiceName)
	assert.Equal(t, pb.ServiceProvisioningStatus_STATUS_PENDING, resp.Services[2].Status)
	assert.Empty(t, resp.Services[2].MigrationVersion)
	assert.Nil(t, resp.Services[2].StartedAt)
	assert.Nil(t, resp.Services[2].CompletedAt)
}

func TestService_GetTenantProvisioningStatus_TenantNotFound(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.GetTenantProvisioningStatusRequest{
		TenantId: "nonexistent_tenant",
	}

	_, err := svc.GetTenantProvisioningStatus(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
	assert.Contains(t, st.Message(), "nonexistent_tenant not found")
}

func TestService_GetTenantProvisioningStatus_EmptyServicesList(t *testing.T) {
	svc, db, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create tenant
	createReq := &pb.InitiateTenantRequest{
		TenantId:        "empty_services_test",
		DisplayName:     "Empty Services Test",
		SettlementAsset: "USD",
	}
	_, err := svc.InitiateTenant(ctx, createReq)
	require.NoError(t, err)

	// Auto-migrate the provisioning status table but don't insert any records
	err = db.AutoMigrate(&persistence.ProvisioningStatusEntity{})
	require.NoError(t, err)

	// Get provisioning status
	req := &pb.GetTenantProvisioningStatusRequest{
		TenantId: "empty_services_test",
	}
	resp, err := svc.GetTenantProvisioningStatus(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Should return empty services array, not an error
	assert.Equal(t, "empty_services_test", resp.TenantId)
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_ACTIVE, resp.OverallStatus)
	assert.Empty(t, resp.Services)
	assert.Empty(t, resp.ErrorMessage)
}

func TestService_GetTenantProvisioningStatus_WithFailedService(t *testing.T) {
	svc, db, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create tenant with provisioning_failed status
	createReq := &pb.InitiateTenantRequest{
		TenantId:        "failed_provisioning_test",
		DisplayName:     "Failed Provisioning Test",
		SettlementAsset: "EUR",
	}
	createResp, err := svc.InitiateTenant(ctx, createReq)
	require.NoError(t, err)

	// Update tenant status to provisioning_failed
	tenantID, err := tenant.NewTenantID(createResp.Tenant.TenantId)
	require.NoError(t, err)
	_, err = svc.repo.UpdateStatusWithError(ctx, tenantID, domain.StatusProvisioningFailed, "Database connection timeout", int(createResp.Tenant.Version))
	require.NoError(t, err)

	// Auto-migrate provisioning status table
	err = db.AutoMigrate(&persistence.ProvisioningStatusEntity{})
	require.NoError(t, err)

	// Insert failed service status via GORM
	failedStarted := time.Now().Add(-5 * time.Minute)
	failedCompleted := time.Now()
	err = db.Create(&persistence.ProvisioningStatusEntity{
		TenantID:     "failed_provisioning_test",
		ServiceName:  "party",
		Status:       string(domain.ServiceStatusFailed),
		ErrorMessage: stringPtr("Migration 003 failed: constraint violation"),
		StartedAt:    &failedStarted,
		CompletedAt:  &failedCompleted,
	}).Error
	require.NoError(t, err)

	// Get provisioning status
	req := &pb.GetTenantProvisioningStatusRequest{
		TenantId: "failed_provisioning_test",
	}
	resp, err := svc.GetTenantProvisioningStatus(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp)

	// Verify overall status is provisioning_failed
	assert.Equal(t, "failed_provisioning_test", resp.TenantId)
	assert.Equal(t, pb.TenantStatus_TENANT_STATUS_PROVISIONING_FAILED, resp.OverallStatus)
	assert.Equal(t, "Database connection timeout", resp.ErrorMessage)

	// Verify failed service details
	require.Len(t, resp.Services, 1)
	assert.Equal(t, "party", resp.Services[0].ServiceName)
	assert.Equal(t, pb.ServiceProvisioningStatus_STATUS_FAILED, resp.Services[0].Status)
	assert.Equal(t, "Migration 003 failed: constraint violation", resp.Services[0].ErrorMessage)
	assert.NotNil(t, resp.Services[0].StartedAt)
	assert.NotNil(t, resp.Services[0].CompletedAt)
}

func TestService_GetTenantProvisioningStatus_InvalidTenantID(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.GetTenantProvisioningStatusRequest{
		TenantId: "invalid-tenant-id-with-dashes",
	}

	_, err := svc.GetTenantProvisioningStatus(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
	assert.Contains(t, st.Message(), "invalid tenant ID")
}

func TestService_toProtoServiceStatus(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	tests := []struct {
		name          string
		domainStatus  domain.ServiceProvisioningStatus
		expectedProto pb.ServiceProvisioningStatus_Status
	}{
		{
			name:          "pending to proto",
			domainStatus:  domain.ServiceStatusPending,
			expectedProto: pb.ServiceProvisioningStatus_STATUS_PENDING,
		},
		{
			name:          "in_progress to proto",
			domainStatus:  domain.ServiceStatusInProgress,
			expectedProto: pb.ServiceProvisioningStatus_STATUS_IN_PROGRESS,
		},
		{
			name:          "completed to proto",
			domainStatus:  domain.ServiceStatusCompleted,
			expectedProto: pb.ServiceProvisioningStatus_STATUS_COMPLETED,
		},
		{
			name:          "failed to proto",
			domainStatus:  domain.ServiceStatusFailed,
			expectedProto: pb.ServiceProvisioningStatus_STATUS_FAILED,
		},
		{
			name:          "unknown status to unspecified",
			domainStatus:  domain.ServiceProvisioningStatus("unknown"),
			expectedProto: pb.ServiceProvisioningStatus_STATUS_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.toProtoServiceStatus(tt.domainStatus)
			assert.Equal(t, tt.expectedProto, result)
		})
	}
}

// stringPtr is a helper function to create a pointer to a string.
func stringPtr(s string) *string {
	return &s
}
