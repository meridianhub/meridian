package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	tenantv1 "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	gateway "github.com/meridianhub/meridian/services/api-gateway"
	identitydomain "github.com/meridianhub/meridian/services/identity/domain"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// stubTenantServiceClient is a minimal stub implementing TenantServiceClient
// for testing the loopbackTenantCreator adapter.
type stubTenantServiceClient struct {
	tenantv1.TenantServiceClient
	initiateCalled bool
	initiateReq    *tenantv1.InitiateTenantRequest
	initiateResp   *tenantv1.InitiateTenantResponse
	initiateErr    error

	updateStatusCalled bool
	updateStatusReq    *tenantv1.UpdateTenantStatusRequest
	updateStatusResp   *tenantv1.UpdateTenantStatusResponse
	updateStatusErr    error
}

func (s *stubTenantServiceClient) InitiateTenant(_ context.Context, req *tenantv1.InitiateTenantRequest, _ ...grpc.CallOption) (*tenantv1.InitiateTenantResponse, error) {
	s.initiateCalled = true
	s.initiateReq = req
	if s.initiateErr != nil {
		return nil, s.initiateErr
	}
	return s.initiateResp, nil
}

func (s *stubTenantServiceClient) UpdateTenantStatus(_ context.Context, req *tenantv1.UpdateTenantStatusRequest, _ ...grpc.CallOption) (*tenantv1.UpdateTenantStatusResponse, error) {
	s.updateStatusCalled = true
	s.updateStatusReq = req
	if s.updateStatusErr != nil {
		return nil, s.updateStatusErr
	}
	return s.updateStatusResp, nil
}

func TestLoopbackTenantCreator_CreateTenant(t *testing.T) {
	stub := &stubTenantServiceClient{
		initiateResp: &tenantv1.InitiateTenantResponse{
			Tenant: &tenantv1.Tenant{TenantId: "acme_corp"},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	creator := &loopbackTenantCreator{
		client:     stub,
		baseDomain: "demo.meridianhub.cloud",
		logger:     logger,
	}

	tenantID, err := creator.CreateTenant(context.Background(), "acme_corp", "acme-corp", "Acme Corp")

	require.NoError(t, err)
	assert.Equal(t, "acme_corp", tenantID)
	assert.True(t, stub.initiateCalled)
	assert.Equal(t, "acme_corp", stub.initiateReq.TenantId)
	assert.Equal(t, "Acme Corp", stub.initiateReq.DisplayName)
	assert.Equal(t, "acme-corp", stub.initiateReq.Slug)
	assert.Equal(t, "acme-corp.demo.meridianhub.cloud", stub.initiateReq.Subdomain,
		"subdomain should include BASE_DOMAIN suffix")
	assert.NotEmpty(t, stub.initiateReq.SettlementAsset, "settlement_asset must be set (proto requires it)")
}

func TestLoopbackTenantCreator_CreateTenant_EmptyBaseDomain(t *testing.T) {
	stub := &stubTenantServiceClient{
		initiateResp: &tenantv1.InitiateTenantResponse{
			Tenant: &tenantv1.Tenant{TenantId: "acme_corp"},
		},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	creator := &loopbackTenantCreator{
		client:     stub,
		baseDomain: "",
		logger:     logger,
	}

	_, err := creator.CreateTenant(context.Background(), "acme_corp", "acme-corp", "Acme Corp")

	require.NoError(t, err)
	assert.Equal(t, "acme-corp", stub.initiateReq.Subdomain,
		"when baseDomain is empty, subdomain should be just the slug")
}

func TestLoopbackTenantCreator_CreateTenant_NilTenantInResponse(t *testing.T) {
	stub := &stubTenantServiceClient{
		initiateResp: &tenantv1.InitiateTenantResponse{Tenant: nil},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	creator := &loopbackTenantCreator{client: stub, logger: logger}

	_, err := creator.CreateTenant(context.Background(), "acme_corp", "acme-corp", "Acme Corp")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil tenant")
}

func TestLoopbackTenantCreator_DeleteTenant(t *testing.T) {
	stub := &stubTenantServiceClient{
		updateStatusResp: &tenantv1.UpdateTenantStatusResponse{},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	creator := &loopbackTenantCreator{client: stub, logger: logger}

	err := creator.DeleteTenant(context.Background(), "acme_corp")

	require.NoError(t, err)
	assert.True(t, stub.updateStatusCalled)
	assert.Equal(t, "acme_corp", stub.updateStatusReq.TenantId)
	assert.Equal(t, tenantv1.TenantStatus_TENANT_STATUS_DEPROVISIONED, stub.updateStatusReq.Status)
}

// TestRegistrationEndpoint_Reachable verifies that when the RegistrationHandler
// is wired into the gateway server, POST /api/v1/register returns a non-401
// response (proving the route is registered and not falling through to the
// auth-protected catch-all).
func TestRegistrationEndpoint_Reachable(t *testing.T) {
	t.Setenv("AUTH_ENABLED", "false")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	httpPort := allocateFreePort(t)

	// Build a stub TenantCreator that returns a canned tenant ID.
	stubCreator := &loopbackTenantCreator{
		client: &stubTenantServiceClient{
			initiateResp: &tenantv1.InitiateTenantResponse{
				Tenant: &tenantv1.Tenant{TenantId: "test_org"},
			},
		},
		logger: logger,
	}

	// Create a registration handler with a nil identity repo.
	// The request will fail at identity provisioning (expected), but crucially
	// it should NOT return 401 - proving the route is registered.
	handler, err := gateway.NewRegistrationHandler(gateway.RegistrationHandlerConfig{
		TenantCreator: stubCreator,
		IdentityRepo:  &noopIdentityRepo{},
		BaseDomain:    "test.local",
		Logger:        logger,
	})
	require.NoError(t, err)

	config := &gateway.Config{Port: httpPort}
	srv := gateway.NewServer(config, logger, nil,
		gateway.WithRegistrationHandler(handler),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() { _ = srv.Start(ctx) }()

	// Wait for server to bind.
	addr := fmt.Sprintf("localhost:%d", httpPort)
	err = await.UntilNoError(func() error {
		conn, dialErr := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
		if dialErr != nil {
			return dialErr
		}
		conn.Close()
		return nil
	})
	require.NoError(t, err, "gateway server did not start")

	body, _ := json.Marshal(map[string]string{
		"slug":     "test-org",
		"email":    "admin@test.org",
		"password": "StrongPass123!",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		fmt.Sprintf("http://localhost:%d/api/v1/register", httpPort),
		bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// The key assertion: NOT 401 (route is reachable, not blocked by auth).
	// We accept any status that isn't 401 - the actual business logic may
	// return 500 (identity repo is a noop) or 201 (if the stub works end-to-end).
	assert.NotEqual(t, http.StatusUnauthorized, resp.StatusCode,
		"registration endpoint should not require authentication")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}

// noopIdentityRepo satisfies identitydomain.Repository for the integration test.
// SaveIdentityWithRoles succeeds; all other methods return errors.
type noopIdentityRepo struct{}

func (n *noopIdentityRepo) Save(_ context.Context, _ *identitydomain.Identity) error {
	return fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) FindByID(_ context.Context, _ uuid.UUID) (*identitydomain.Identity, error) {
	return nil, fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) FindByEmail(_ context.Context, _ string) (*identitydomain.Identity, error) {
	return nil, fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) ListByTenant(_ context.Context) ([]*identitydomain.Identity, error) {
	return nil, fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) SaveRoleAssignment(_ context.Context, _ *identitydomain.RoleAssignment) error {
	return fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) FindRoleAssignments(_ context.Context, _ uuid.UUID) ([]*identitydomain.RoleAssignment, error) {
	return nil, fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) SaveIdentityWithInvitation(_ context.Context, _ *identitydomain.Identity, _ *identitydomain.Invitation) error {
	return fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) SaveIdentityWithRoles(_ context.Context, _ *identitydomain.Identity, _ []*identitydomain.RoleAssignment) error {
	return nil // success - allows registration to complete
}

func (n *noopIdentityRepo) SaveRoleAssignments(_ context.Context, _ []*identitydomain.RoleAssignment) error {
	return fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) SaveInvitation(_ context.Context, _ *identitydomain.Invitation) error {
	return fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) FindInvitationByTokenHash(_ context.Context, _ string) (*identitydomain.Invitation, error) {
	return nil, fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) SaveVerificationToken(_ context.Context, _ *identitydomain.VerificationToken) error {
	return fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) FindVerificationTokenByHash(_ context.Context, _ string) (*identitydomain.VerificationToken, error) {
	return nil, fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) CountVerificationTokensInWindow(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
	return 0, fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) SavePasswordResetToken(_ context.Context, _ *identitydomain.PasswordResetToken) error {
	return fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) FindPasswordResetTokenByHash(_ context.Context, _ string) (*identitydomain.PasswordResetToken, error) {
	return nil, fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) CountPasswordResetTokensInWindow(_ context.Context, _ uuid.UUID, _ time.Duration) (int, error) {
	return 0, fmt.Errorf("not implemented")
}

func (n *noopIdentityRepo) MarkPasswordResetTokensConsumedForIdentity(_ context.Context, _ uuid.UUID) error {
	return fmt.Errorf("not implemented")
}
