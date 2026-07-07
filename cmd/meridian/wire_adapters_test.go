package main

import (
	"context"
	"testing"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// loopbackServerApplyClient simulates the loopback ApplyManifestService server.
// A real loopback ApplyManifest is a gRPC round-trip back through the SAME
// unified-binary server, so the caller's OUTGOING metadata arrives as INCOMING
// metadata and is subject to the gateway RBAC bridge. This fake reproduces that:
// it re-frames outgoing metadata as incoming and runs it through the real
// GatewayManifestRBAC + control-plane RBAC interceptor, capturing the principal
// the inner apply observes. It proves the adapter forwards the verified identity.
type loopbackServerApplyClient struct {
	controlplanev1.ApplyManifestServiceClient // embedded; only ApplyManifest overridden
	interceptor                               grpc.UnaryServerInterceptor
	gotClaims                                 *auth.Claims
}

func (f *loopbackServerApplyClient) ApplyManifest(
	ctx context.Context,
	in *controlplanev1.ApplyManifestRequest,
	_ ...grpc.CallOption,
) (*controlplanev1.ApplyManifestResponse, error) {
	// Outgoing (client-side) metadata becomes incoming (server-side) metadata on
	// a real gRPC hop.
	md, _ := metadata.FromOutgoingContext(ctx)
	// A real gRPC hop starts a fresh server-side context; the client's context is
	// deliberately not inherited here.
	inCtx := metadata.NewIncomingContext(context.Background(), md)

	//nolint:contextcheck // simulates a server-side gRPC hop with a fresh context
	resp, err := f.interceptor(inCtx, in,
		&grpc.UnaryServerInfo{FullMethod: methodApplyManifest},
		func(c context.Context, _ interface{}) (interface{}, error) {
			f.gotClaims = claimsFromGatewayMetadata(c)
			return &controlplanev1.ApplyManifestResponse{
				Status: controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED,
			}, nil
		})
	if err != nil {
		return nil, err
	}
	return resp.(*controlplanev1.ApplyManifestResponse), nil
}

// The loopback ApplyManifest adapter forwards the caller's gateway-verified
// identity so the inner (RBAC-guarded) apply is authorized as the same admin who
// passed the outer RollbackManifest RBAC. This is the SEC-2 rollback regression
// guard: before forwarding, the inner apply saw no principal and RBAC denied it,
// so RollbackManifest always returned FAILED.
func TestLoopbackApplyAdapter_ForwardsAdminIdentity(t *testing.T) {
	fake := &loopbackServerApplyClient{interceptor: unaryInterceptor()}
	adapter := loopbackApplyManifestAdapter{c: fake}

	// The RollbackManifest handler's context carries the incoming gateway identity.
	ctx := ctxWithGatewayIdentity("admin-user", "admin")
	resp, err := adapter.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{})

	require.NoError(t, err)
	assert.Equal(t, controlplanev1.ApplyManifestStatus_APPLY_MANIFEST_STATUS_APPLIED, resp.Status)
	require.NotNil(t, fake.gotClaims, "inner apply must observe the forwarded principal")
	assert.Equal(t, "admin-user", fake.gotClaims.UserID)
	assert.Equal(t, []string{"admin"}, fake.gotClaims.Roles)
	assert.Equal(t, "org_test", fake.gotClaims.TenantID)
}

// A non-admin caller that passes the outer RBAC would never reach the inner apply
// in practice, but the loopback path must still fail closed if it does: the
// forwarded identity carries a non-admin role and the inner RBAC denies it.
func TestLoopbackApplyAdapter_NonAdminFailsClosed(t *testing.T) {
	fake := &loopbackServerApplyClient{interceptor: unaryInterceptor()}
	adapter := loopbackApplyManifestAdapter{c: fake}

	ctx := ctxWithGatewayIdentity("auditor-user", "auditor")
	_, err := adapter.ApplyManifest(ctx, &controlplanev1.ApplyManifestRequest{})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.PermissionDenied, st.Code())
	assert.Nil(t, fake.gotClaims, "handler must not run when RBAC denies")
}

// With no principal on the incoming context, nothing is forwarded and the inner
// apply's RBAC fails closed with Unauthenticated.
func TestLoopbackApplyAdapter_NoPrincipalFailsClosed(t *testing.T) {
	fake := &loopbackServerApplyClient{interceptor: unaryInterceptor()}
	adapter := loopbackApplyManifestAdapter{c: fake}

	_, err := adapter.ApplyManifest(context.Background(), &controlplanev1.ApplyManifestRequest{})

	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.Unauthenticated, st.Code())
	assert.Nil(t, fake.gotClaims, "handler must not run without a principal")
}

// forwardGatewayIdentity copies only the three gateway-verified identity keys and
// drops arbitrary client metadata, so the loopback call cannot smuggle extra
// metadata into the inner apply.
func TestForwardGatewayIdentity_OnlyForwardsIdentityKeys(t *testing.T) {
	in := metadata.NewIncomingContext(context.Background(), metadata.Pairs(
		mdKeyUserID, "user-1",
		mdKeyAuthRoles, "admin",
		mdKeyTenantID, "org_test",
		"x-spoofed-header", "attacker",
	))

	out, ok := metadata.FromOutgoingContext(forwardGatewayIdentity(in))
	require.True(t, ok)
	assert.Equal(t, []string{"user-1"}, out.Get(mdKeyUserID))
	assert.Equal(t, []string{"admin"}, out.Get(mdKeyAuthRoles))
	assert.Equal(t, []string{"org_test"}, out.Get(mdKeyTenantID))
	assert.Empty(t, out.Get("x-spoofed-header"), "non-identity metadata must not be forwarded")
}

// With no incoming metadata, forwarding is a no-op and no outgoing metadata is set.
func TestForwardGatewayIdentity_NoIncomingMetadata(t *testing.T) {
	_, ok := metadata.FromOutgoingContext(forwardGatewayIdentity(context.Background()))
	assert.False(t, ok, "no outgoing metadata should be created when none is incoming")
}
