package client

import (
	"context"
	"testing"

	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockTenantServiceClient implements tenantv1.TenantServiceClient for testing.
type mockTenantServiceClient struct {
	InitiateTenantFunc              func(ctx context.Context, in *tenantv1.InitiateTenantRequest, opts ...grpc.CallOption) (*tenantv1.InitiateTenantResponse, error)
	RetrieveTenantFunc              func(ctx context.Context, in *tenantv1.RetrieveTenantRequest, opts ...grpc.CallOption) (*tenantv1.RetrieveTenantResponse, error)
	UpdateTenantStatusFunc          func(ctx context.Context, in *tenantv1.UpdateTenantStatusRequest, opts ...grpc.CallOption) (*tenantv1.UpdateTenantStatusResponse, error)
	ListTenantsFunc                 func(ctx context.Context, in *tenantv1.ListTenantsRequest, opts ...grpc.CallOption) (*tenantv1.ListTenantsResponse, error)
	ReconcileMigrationsFunc         func(ctx context.Context, in *tenantv1.ReconcileMigrationsRequest, opts ...grpc.CallOption) (*tenantv1.ReconcileMigrationsResponse, error)
	GetTenantProvisioningStatusFunc func(ctx context.Context, in *tenantv1.GetTenantProvisioningStatusRequest, opts ...grpc.CallOption) (*tenantv1.GetTenantProvisioningStatusResponse, error)
}

func (m *mockTenantServiceClient) InitiateTenant(ctx context.Context, in *tenantv1.InitiateTenantRequest, opts ...grpc.CallOption) (*tenantv1.InitiateTenantResponse, error) {
	if m.InitiateTenantFunc != nil {
		return m.InitiateTenantFunc(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockTenantServiceClient) RetrieveTenant(ctx context.Context, in *tenantv1.RetrieveTenantRequest, opts ...grpc.CallOption) (*tenantv1.RetrieveTenantResponse, error) {
	if m.RetrieveTenantFunc != nil {
		return m.RetrieveTenantFunc(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockTenantServiceClient) UpdateTenantStatus(ctx context.Context, in *tenantv1.UpdateTenantStatusRequest, opts ...grpc.CallOption) (*tenantv1.UpdateTenantStatusResponse, error) {
	if m.UpdateTenantStatusFunc != nil {
		return m.UpdateTenantStatusFunc(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockTenantServiceClient) ListTenants(ctx context.Context, in *tenantv1.ListTenantsRequest, opts ...grpc.CallOption) (*tenantv1.ListTenantsResponse, error) {
	if m.ListTenantsFunc != nil {
		return m.ListTenantsFunc(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockTenantServiceClient) ReconcileMigrations(ctx context.Context, in *tenantv1.ReconcileMigrationsRequest, opts ...grpc.CallOption) (*tenantv1.ReconcileMigrationsResponse, error) {
	if m.ReconcileMigrationsFunc != nil {
		return m.ReconcileMigrationsFunc(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

func (m *mockTenantServiceClient) GetTenantProvisioningStatus(ctx context.Context, in *tenantv1.GetTenantProvisioningStatusRequest, opts ...grpc.CallOption) (*tenantv1.GetTenantProvisioningStatusResponse, error) {
	if m.GetTenantProvisioningStatusFunc != nil {
		return m.GetTenantProvisioningStatusFunc(ctx, in, opts...)
	}
	return nil, status.Error(codes.Unimplemented, "not implemented")
}

// newTestClient creates a Client with an injected mock for unit testing.
func newTestClient(mock tenantv1.TenantServiceClient) *Client {
	return &Client{
		tenant:  mock,
		timeout: DefaultTimeout,
	}
}

// =============================================================================
// InitiateTenant Tests
// =============================================================================

func TestInitiateTenant_Success(t *testing.T) {
	expected := &tenantv1.InitiateTenantResponse{}
	mock := &mockTenantServiceClient{
		InitiateTenantFunc: func(_ context.Context, _ *tenantv1.InitiateTenantRequest, _ ...grpc.CallOption) (*tenantv1.InitiateTenantResponse, error) {
			return expected, nil
		},
	}
	c := newTestClient(mock)

	resp, err := c.InitiateTenant(context.Background(), &tenantv1.InitiateTenantRequest{})
	require.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestInitiateTenant_Error(t *testing.T) {
	mock := &mockTenantServiceClient{
		InitiateTenantFunc: func(_ context.Context, _ *tenantv1.InitiateTenantRequest, _ ...grpc.CallOption) (*tenantv1.InitiateTenantResponse, error) {
			return nil, status.Error(codes.Internal, "db error")
		},
	}
	c := newTestClient(mock)

	_, err := c.InitiateTenant(context.Background(), &tenantv1.InitiateTenantRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "initiate tenant")
}

// =============================================================================
// RetrieveTenant Tests
// =============================================================================

func TestRetrieveTenant_Success(t *testing.T) {
	expected := &tenantv1.RetrieveTenantResponse{}
	mock := &mockTenantServiceClient{
		RetrieveTenantFunc: func(_ context.Context, _ *tenantv1.RetrieveTenantRequest, _ ...grpc.CallOption) (*tenantv1.RetrieveTenantResponse, error) {
			return expected, nil
		},
	}
	c := newTestClient(mock)

	resp, err := c.RetrieveTenant(context.Background(), &tenantv1.RetrieveTenantRequest{})
	require.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestRetrieveTenant_Error(t *testing.T) {
	mock := &mockTenantServiceClient{
		RetrieveTenantFunc: func(_ context.Context, _ *tenantv1.RetrieveTenantRequest, _ ...grpc.CallOption) (*tenantv1.RetrieveTenantResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	c := newTestClient(mock)

	_, err := c.RetrieveTenant(context.Background(), &tenantv1.RetrieveTenantRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "retrieve tenant")
}

// =============================================================================
// UpdateTenantStatus Tests
// =============================================================================

func TestUpdateTenantStatus_Success(t *testing.T) {
	expected := &tenantv1.UpdateTenantStatusResponse{}
	mock := &mockTenantServiceClient{
		UpdateTenantStatusFunc: func(_ context.Context, _ *tenantv1.UpdateTenantStatusRequest, _ ...grpc.CallOption) (*tenantv1.UpdateTenantStatusResponse, error) {
			return expected, nil
		},
	}
	c := newTestClient(mock)

	resp, err := c.UpdateTenantStatus(context.Background(), &tenantv1.UpdateTenantStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestUpdateTenantStatus_Error(t *testing.T) {
	mock := &mockTenantServiceClient{
		UpdateTenantStatusFunc: func(_ context.Context, _ *tenantv1.UpdateTenantStatusRequest, _ ...grpc.CallOption) (*tenantv1.UpdateTenantStatusResponse, error) {
			return nil, status.Error(codes.FailedPrecondition, "invalid transition")
		},
	}
	c := newTestClient(mock)

	_, err := c.UpdateTenantStatus(context.Background(), &tenantv1.UpdateTenantStatusRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "update tenant status")
}

// =============================================================================
// ListTenants Tests
// =============================================================================

func TestListTenants_Success(t *testing.T) {
	expected := &tenantv1.ListTenantsResponse{}
	mock := &mockTenantServiceClient{
		ListTenantsFunc: func(_ context.Context, _ *tenantv1.ListTenantsRequest, _ ...grpc.CallOption) (*tenantv1.ListTenantsResponse, error) {
			return expected, nil
		},
	}
	c := newTestClient(mock)

	resp, err := c.ListTenants(context.Background(), &tenantv1.ListTenantsRequest{})
	require.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestListTenants_Error(t *testing.T) {
	mock := &mockTenantServiceClient{
		ListTenantsFunc: func(_ context.Context, _ *tenantv1.ListTenantsRequest, _ ...grpc.CallOption) (*tenantv1.ListTenantsResponse, error) {
			return nil, status.Error(codes.Internal, "db error")
		},
	}
	c := newTestClient(mock)

	_, err := c.ListTenants(context.Background(), &tenantv1.ListTenantsRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list tenants")
}

// =============================================================================
// ReconcileMigrations Tests
// =============================================================================

func TestReconcileMigrations_Success(t *testing.T) {
	expected := &tenantv1.ReconcileMigrationsResponse{}
	mock := &mockTenantServiceClient{
		ReconcileMigrationsFunc: func(_ context.Context, _ *tenantv1.ReconcileMigrationsRequest, _ ...grpc.CallOption) (*tenantv1.ReconcileMigrationsResponse, error) {
			return expected, nil
		},
	}
	c := newTestClient(mock)

	resp, err := c.ReconcileMigrations(context.Background(), &tenantv1.ReconcileMigrationsRequest{})
	require.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestReconcileMigrations_Error(t *testing.T) {
	mock := &mockTenantServiceClient{
		ReconcileMigrationsFunc: func(_ context.Context, _ *tenantv1.ReconcileMigrationsRequest, _ ...grpc.CallOption) (*tenantv1.ReconcileMigrationsResponse, error) {
			return nil, status.Error(codes.Internal, "migration error")
		},
	}
	c := newTestClient(mock)

	_, err := c.ReconcileMigrations(context.Background(), &tenantv1.ReconcileMigrationsRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "reconcile migrations")
}

// =============================================================================
// GetTenantProvisioningStatus Tests
// =============================================================================

func TestGetTenantProvisioningStatus_Success(t *testing.T) {
	expected := &tenantv1.GetTenantProvisioningStatusResponse{}
	mock := &mockTenantServiceClient{
		GetTenantProvisioningStatusFunc: func(_ context.Context, _ *tenantv1.GetTenantProvisioningStatusRequest, _ ...grpc.CallOption) (*tenantv1.GetTenantProvisioningStatusResponse, error) {
			return expected, nil
		},
	}
	c := newTestClient(mock)

	resp, err := c.GetTenantProvisioningStatus(context.Background(), &tenantv1.GetTenantProvisioningStatusRequest{})
	require.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestGetTenantProvisioningStatus_Error(t *testing.T) {
	mock := &mockTenantServiceClient{
		GetTenantProvisioningStatusFunc: func(_ context.Context, _ *tenantv1.GetTenantProvisioningStatusRequest, _ ...grpc.CallOption) (*tenantv1.GetTenantProvisioningStatusResponse, error) {
			return nil, status.Error(codes.NotFound, "not found")
		},
	}
	c := newTestClient(mock)

	_, err := c.GetTenantProvisioningStatus(context.Background(), &tenantv1.GetTenantProvisioningStatusRequest{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get tenant provisioning status")
}
