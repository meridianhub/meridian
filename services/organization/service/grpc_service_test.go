package service

import (
	"context"
	"log/slog"
	"os"
	"testing"

	pb "github.com/meridianhub/meridian/api/proto/meridian/organization/v1"
	"github.com/meridianhub/meridian/services/organization/adapters/persistence"
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
	db, cleanup := testdb.SetupPostgres(t, []interface{}{&persistence.OrganizationEntity{}})
	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := NewService(repo, logger)
	return svc, db, cleanup
}

func TestService_InitiateOrganization(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateOrganizationRequest{
		OrganizationId:  "test_org",
		DisplayName:     "Test Organization",
		SettlementAsset: "GBP",
	}

	resp, err := svc.InitiateOrganization(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Organization)

	assert.Equal(t, "test_org", resp.Organization.OrganizationId)
	assert.Equal(t, "Test Organization", resp.Organization.DisplayName)
	assert.Equal(t, "GBP", resp.Organization.SettlementAsset)
	assert.Equal(t, pb.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE, resp.Organization.Status)
	assert.Equal(t, int32(1), resp.Organization.Version)
	assert.NotNil(t, resp.Organization.CreatedAt)
}

func TestService_InitiateOrganization_WithSubdomain(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateOrganizationRequest{
		OrganizationId:  "acme_bank",
		DisplayName:     "Acme Bank",
		SettlementAsset: "USD",
		Subdomain:       "acme-bank.demo.meridian.io",
	}

	resp, err := svc.InitiateOrganization(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, "acme-bank.demo.meridian.io", resp.Organization.Subdomain)
}

func TestService_InitiateOrganization_WithMetadata(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	metadata, _ := structpb.NewStruct(map[string]interface{}{
		"tier":     "enterprise",
		"features": []interface{}{"multi-currency"},
	})

	req := &pb.InitiateOrganizationRequest{
		OrganizationId:  "enterprise_org",
		DisplayName:     "Enterprise Org",
		SettlementAsset: "GBP",
		Metadata:        metadata,
	}

	resp, err := svc.InitiateOrganization(ctx, req)
	require.NoError(t, err)
	require.NotNil(t, resp.Organization.Metadata)
	assert.Equal(t, "enterprise", resp.Organization.Metadata.Fields["tier"].GetStringValue())
}

func TestService_InitiateOrganization_InvalidID(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateOrganizationRequest{
		OrganizationId:  "invalid-id-with-dashes", // Dashes not allowed
		DisplayName:     "Invalid Org",
		SettlementAsset: "GBP",
	}

	_, err := svc.InitiateOrganization(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestService_InitiateOrganization_Duplicate(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.InitiateOrganizationRequest{
		OrganizationId:  "duplicate_org",
		DisplayName:     "First Org",
		SettlementAsset: "GBP",
	}

	// Create first organization
	_, err := svc.InitiateOrganization(ctx, req)
	require.NoError(t, err)

	// Try to create duplicate
	req.DisplayName = "Second Org"
	_, err = svc.InitiateOrganization(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.AlreadyExists, st.Code())
}

func TestService_RetrieveOrganization(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create organization
	createReq := &pb.InitiateOrganizationRequest{
		OrganizationId:  "retrieve_test",
		DisplayName:     "Retrieve Test",
		SettlementAsset: "GBP",
	}
	_, err := svc.InitiateOrganization(ctx, createReq)
	require.NoError(t, err)

	// Retrieve organization
	retrieveReq := &pb.RetrieveOrganizationRequest{
		OrganizationId: "retrieve_test",
	}
	resp, err := svc.RetrieveOrganization(ctx, retrieveReq)
	require.NoError(t, err)
	assert.Equal(t, "retrieve_test", resp.Organization.OrganizationId)
	assert.Equal(t, "Retrieve Test", resp.Organization.DisplayName)
}

func TestService_RetrieveOrganization_NotFound(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.RetrieveOrganizationRequest{
		OrganizationId: "nonexistent",
	}

	_, err := svc.RetrieveOrganization(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestService_UpdateOrganizationStatus(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create organization
	createReq := &pb.InitiateOrganizationRequest{
		OrganizationId:  "status_test",
		DisplayName:     "Status Test",
		SettlementAsset: "GBP",
	}
	_, err := svc.InitiateOrganization(ctx, createReq)
	require.NoError(t, err)

	// Update status to suspended
	updateReq := &pb.UpdateOrganizationStatusRequest{
		OrganizationId: "status_test",
		Status:         pb.OrganizationStatus_ORGANIZATION_STATUS_SUSPENDED,
	}
	resp, err := svc.UpdateOrganizationStatus(ctx, updateReq)
	require.NoError(t, err)
	assert.Equal(t, pb.OrganizationStatus_ORGANIZATION_STATUS_SUSPENDED, resp.Organization.Status)
	assert.Equal(t, int32(2), resp.Organization.Version)
}

func TestService_UpdateOrganizationStatus_ToDeprovisioned(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create organization
	createReq := &pb.InitiateOrganizationRequest{
		OrganizationId:  "deprov_test",
		DisplayName:     "Deprov Test",
		SettlementAsset: "GBP",
	}
	_, err := svc.InitiateOrganization(ctx, createReq)
	require.NoError(t, err)

	// Update status to deprovisioned
	updateReq := &pb.UpdateOrganizationStatusRequest{
		OrganizationId: "deprov_test",
		Status:         pb.OrganizationStatus_ORGANIZATION_STATUS_DEPROVISIONED,
	}
	resp, err := svc.UpdateOrganizationStatus(ctx, updateReq)
	require.NoError(t, err)
	assert.Equal(t, pb.OrganizationStatus_ORGANIZATION_STATUS_DEPROVISIONED, resp.Organization.Status)
	assert.NotNil(t, resp.Organization.DeprovisionedAt)
}

func TestService_UpdateOrganizationStatus_NotFound(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()
	req := &pb.UpdateOrganizationStatusRequest{
		OrganizationId: "nonexistent",
		Status:         pb.OrganizationStatus_ORGANIZATION_STATUS_SUSPENDED,
	}

	_, err := svc.UpdateOrganizationStatus(ctx, req)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())
}

func TestService_UpdateOrganizationStatus_InvalidStatus(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create organization
	createReq := &pb.InitiateOrganizationRequest{
		OrganizationId:  "invalid_status_test",
		DisplayName:     "Invalid Status Test",
		SettlementAsset: "GBP",
	}
	_, err := svc.InitiateOrganization(ctx, createReq)
	require.NoError(t, err)

	// Try to update with unspecified status
	updateReq := &pb.UpdateOrganizationStatusRequest{
		OrganizationId: "invalid_status_test",
		Status:         pb.OrganizationStatus_ORGANIZATION_STATUS_UNSPECIFIED,
	}
	_, err = svc.UpdateOrganizationStatus(ctx, updateReq)
	require.Error(t, err)

	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, st.Code())
}

func TestService_ListOrganizations(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create 5 organizations
	for i := 0; i < 5; i++ {
		req := &pb.InitiateOrganizationRequest{
			OrganizationId:  "list_org_" + string(rune('a'+i)),
			DisplayName:     "List Org " + string(rune('A'+i)),
			SettlementAsset: "GBP",
		}
		_, err := svc.InitiateOrganization(ctx, req)
		require.NoError(t, err)
	}

	// List all organizations
	listReq := &pb.ListOrganizationsRequest{
		PageSize: 10,
	}
	resp, err := svc.ListOrganizations(ctx, listReq)
	require.NoError(t, err)
	assert.Len(t, resp.Organizations, 5)
}

func TestService_ListOrganizations_WithStatusFilter(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create 3 active organizations
	for i := 0; i < 3; i++ {
		req := &pb.InitiateOrganizationRequest{
			OrganizationId:  "active_" + string(rune('a'+i)),
			DisplayName:     "Active " + string(rune('A'+i)),
			SettlementAsset: "GBP",
		}
		_, err := svc.InitiateOrganization(ctx, req)
		require.NoError(t, err)
	}

	// Suspend 2 of them
	for i := 0; i < 2; i++ {
		updateReq := &pb.UpdateOrganizationStatusRequest{
			OrganizationId: "active_" + string(rune('a'+i)),
			Status:         pb.OrganizationStatus_ORGANIZATION_STATUS_SUSPENDED,
		}
		_, err := svc.UpdateOrganizationStatus(ctx, updateReq)
		require.NoError(t, err)
	}

	// List only active organizations
	listReq := &pb.ListOrganizationsRequest{
		StatusFilter: pb.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE,
		PageSize:     10,
	}
	resp, err := svc.ListOrganizations(ctx, listReq)
	require.NoError(t, err)
	assert.Len(t, resp.Organizations, 1)
	assert.Equal(t, pb.OrganizationStatus_ORGANIZATION_STATUS_ACTIVE, resp.Organizations[0].Status)
}

func TestService_ListOrganizations_Pagination(t *testing.T) {
	svc, _, cleanup := setupTest(t)
	defer cleanup()

	ctx := context.Background()

	// Create 10 organizations
	for i := 0; i < 10; i++ {
		req := &pb.InitiateOrganizationRequest{
			OrganizationId:  "page_org_" + string(rune('a'+i)),
			DisplayName:     "Page Org " + string(rune('A'+i)),
			SettlementAsset: "GBP",
		}
		_, err := svc.InitiateOrganization(ctx, req)
		require.NoError(t, err)
	}

	// Get first page of 3
	listReq := &pb.ListOrganizationsRequest{
		PageSize: 3,
	}
	resp, err := svc.ListOrganizations(ctx, listReq)
	require.NoError(t, err)
	assert.Len(t, resp.Organizations, 3)
	assert.NotEmpty(t, resp.NextPageToken)

	// Get second page
	listReq.PageToken = resp.NextPageToken
	resp2, err := svc.ListOrganizations(ctx, listReq)
	require.NoError(t, err)
	assert.Len(t, resp2.Organizations, 3)

	// Verify no overlap
	for _, org1 := range resp.Organizations {
		for _, org2 := range resp2.Organizations {
			assert.NotEqual(t, org1.OrganizationId, org2.OrganizationId, "Pages should not overlap")
		}
	}
}
