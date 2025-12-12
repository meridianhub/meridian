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
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/shared/platform/testdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
	"gorm.io/gorm"
)

func setupTest(t *testing.T) (*Service, *gorm.DB, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
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
	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := NewService(repo, nil, partyClient, logger)
	return svc, db, cleanup
}

func setupTestWithProvisioner(t *testing.T, mockProv *provisioner.MockProvisioner) (*Service, *gorm.DB, func()) {
	t.Helper()
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.TenantEntity{}})
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
