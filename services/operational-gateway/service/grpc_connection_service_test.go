package service

import (
	"testing"

	"github.com/google/uuid"
	opgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/operational_gateway/v1"
	"github.com/meridianhub/meridian/services/operational-gateway/domain"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ========== TestConnection ==========

// TestTestConnection_ExistingConnection_ReturnsUnimplemented verifies that when a connection exists,
// TestConnection returns Unimplemented with a Phase 2 message (placeholder for live health check).
func TestTestConnection_ExistingConnection_ReturnsUnimplemented(t *testing.T) {
	svc, connRepo := newTestConnService(t)
	ctx := tenantContext("test-tenant")
	tid := tenantIDToUUID(tenant.TenantID("test-tenant"))
	connID := uuid.New().String()

	conn := &domain.ProviderConnection{
		TenantID:     tid,
		ConnectionID: connID,
		ProviderName: "TestProvider",
		Protocol:     domain.ProtocolHTTPS,
		BaseURL:      "https://api.example.com",
	}
	connRepo.connections[tid+":"+connID] = conn

	_, err := svc.TestConnection(ctx, &opgatewayv1.TestConnectionRequest{
		ConnectionId: connID,
	})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unimplemented, st.Code())
	assert.Contains(t, st.Message(), "Phase 2")
}

// ========== UpsertConnection credential type coverage ==========

// TestUpsertConnection_BasicAuth verifies basic auth credentials are accepted.
func TestUpsertConnection_BasicAuth(t *testing.T) {
	svc, _ := newTestConnService(t)
	ctx := tenantContext("test-tenant")

	resp, err := svc.UpsertConnection(ctx, &opgatewayv1.UpsertConnectionRequest{
		ProviderName: "ModulrFinance",
		ProviderType: "payment_gateway",
		Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
		BaseUrl:      "https://api.modulrfinance.com",
		AuthConfig: &opgatewayv1.UpsertConnectionRequest_Basic{
			Basic: &opgatewayv1.BasicAuth{
				Username:          "api-user",
				PasswordSecretRef: "modulr-password",
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Connection)
	assert.Equal(t, "ModulrFinance", resp.Connection.ProviderName)
	assert.Equal(t, opgatewayv1.Protocol_PROTOCOL_HTTPS, resp.Connection.Protocol)
}

// TestUpsertConnection_OAuth2 verifies OAuth2 client credentials are accepted.
func TestUpsertConnection_OAuth2(t *testing.T) {
	svc, _ := newTestConnService(t)
	ctx := tenantContext("test-tenant")

	resp, err := svc.UpsertConnection(ctx, &opgatewayv1.UpsertConnectionRequest{
		ProviderName: "OAuth2Provider",
		ProviderType: "data_provider",
		Protocol:     opgatewayv1.Protocol_PROTOCOL_HTTPS,
		BaseUrl:      "https://api.oauth2provider.com",
		AuthConfig: &opgatewayv1.UpsertConnectionRequest_Oauth2{
			Oauth2: &opgatewayv1.OAuth2Auth{
				TokenUrl:        "https://auth.example.com/token",
				ClientId:        "client-id",
				ClientSecretRef: "client-secret-ref",
			},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, resp.Connection)
	assert.Equal(t, "OAuth2Provider", resp.Connection.ProviderName)
}

// ========== ListConnections error path ==========

// TestListConnections_RepoListError verifies Internal is returned when the repo list fails.
func TestListConnections_RepoListError(t *testing.T) {
	svc, connRepo := newTestConnService(t)
	connRepo.listErr = assert.AnError
	ctx := tenantContext("test-tenant")

	_, err := svc.ListConnections(ctx, &opgatewayv1.ListConnectionsRequest{})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Internal, st.Code())
}
