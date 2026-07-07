package main

import (
	"context"
	"testing"

	controlplaneservice "github.com/meridianhub/meridian/services/control-plane/service"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	methodApplyManifest    = "/meridian.control_plane.v1.ApplyManifestService/ApplyManifest"
	methodRollbackManifest = "/meridian.control_plane.v1.ManifestHistoryService/RollbackManifest"
	methodExecuteSaga      = "/meridian.control_plane.v1.SagaExecutionService/ExecuteSaga"
	methodGetCurrent       = "/meridian.control_plane.v1.ManifestHistoryService/GetCurrentManifest"
)

func okUnaryHandler(_ context.Context, _ interface{}) (interface{}, error) {
	return "ok", nil
}

// unaryInterceptor composes the gateway-identity bridge with the real
// control-plane RBAC interceptor, mirroring the wiring in main.go.
func unaryInterceptor() grpc.UnaryServerInterceptor {
	return GatewayManifestRBACUnaryInterceptor(controlplaneservice.ManifestRBACUnaryInterceptor())
}

// ctxWithGatewayIdentity builds a gRPC context carrying the identity metadata the
// API gateway propagates after validating a caller's JWT.
func ctxWithGatewayIdentity(userID string, roles ...string) context.Context {
	pairs := []string{mdKeyUserID, userID, mdKeyTenantID, "org_test"}
	if len(roles) > 0 {
		joined := roles[0]
		for _, r := range roles[1:] {
			joined += "," + r
		}
		pairs = append(pairs, mdKeyAuthRoles, joined)
	}
	md := metadata.Pairs(pairs...)
	return metadata.NewIncomingContext(context.Background(), md)
}

func assertCode(t *testing.T, err error, want codes.Code) {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, want, st.Code())
}

func assertPermissionDenied(t *testing.T, err error) {
	t.Helper()
	assertCode(t, err, codes.PermissionDenied)
}

// (a) Unauthenticated callers are denied on every mutating manifest RPC. This is
// the SEC-2 regression guard: manifest RPCs must never run without authorization
// on the unified binary. A caller with no propagated principal yields Unauthenticated;
// a caller with an identity but no authorizing role yields PermissionDenied. Both
// are hard denials.
func TestGatewayManifestRBAC_UnauthenticatedDenied(t *testing.T) {
	interceptor := unaryInterceptor()

	for _, method := range []string{methodApplyManifest, methodRollbackManifest, methodExecuteSaga} {
		t.Run(method, func(t *testing.T) {
			// No metadata at all -> no principal -> Unauthenticated.
			_, err := interceptor(context.Background(), nil,
				&grpc.UnaryServerInfo{FullMethod: method}, okUnaryHandler)
			assertCode(t, err, codes.Unauthenticated)

			// Identity present but no roles (authenticated, unauthorized).
			ctx := ctxWithGatewayIdentity("user-1")
			_, err = interceptor(ctx, nil,
				&grpc.UnaryServerInfo{FullMethod: method}, okUnaryHandler)
			assertPermissionDenied(t, err)
		})
	}
}

// (b) Non-admin roles are denied on admin-gated manifest RPCs.
func TestGatewayManifestRBAC_NonAdminDenied(t *testing.T) {
	interceptor := unaryInterceptor()

	cases := []struct {
		method string
		role   string
	}{
		{methodApplyManifest, "auditor"},
		{methodApplyManifest, "operator"},
		{methodRollbackManifest, "auditor"},
		{methodRollbackManifest, "operator"},
		{methodExecuteSaga, "auditor"}, // ExecuteSaga requires operator
	}

	for _, tc := range cases {
		t.Run(tc.method+"/"+tc.role, func(t *testing.T) {
			ctx := ctxWithGatewayIdentity("user-1", tc.role)
			_, err := interceptor(ctx, nil,
				&grpc.UnaryServerInfo{FullMethod: tc.method}, okUnaryHandler)
			assertPermissionDenied(t, err)
		})
	}
}

// (c) Admin role succeeds on all mutating manifest RPCs; operator succeeds on
// ExecuteSaga.
func TestGatewayManifestRBAC_AuthorizedRolesSucceed(t *testing.T) {
	interceptor := unaryInterceptor()

	cases := []struct {
		method string
		role   string
	}{
		{methodApplyManifest, "admin"},
		{methodRollbackManifest, "admin"},
		{methodExecuteSaga, "admin"},
		{methodExecuteSaga, "operator"},
		{methodGetCurrent, "auditor"},
	}

	for _, tc := range cases {
		t.Run(tc.method+"/"+tc.role, func(t *testing.T) {
			ctx := ctxWithGatewayIdentity("user-1", tc.role)
			resp, err := interceptor(ctx, nil,
				&grpc.UnaryServerInfo{FullMethod: tc.method}, okUnaryHandler)
			require.NoError(t, err)
			assert.Equal(t, "ok", resp)
		})
	}
}

// Non-control-plane methods pass through untouched regardless of identity, so the
// fix does not regress other services on the unified binary.
func TestGatewayManifestRBAC_NonControlPlanePassthrough(t *testing.T) {
	interceptor := unaryInterceptor()

	for _, method := range []string{
		"/meridian.party.v1.PartyService/CreateParty",
		"/meridian.identity.v1.IdentityService/Login",
		"/grpc.health.v1.Health/Check",
	} {
		t.Run(method, func(t *testing.T) {
			// Even with no identity, non-control-plane methods are allowed through.
			resp, err := interceptor(context.Background(), nil,
				&grpc.UnaryServerInfo{FullMethod: method}, okUnaryHandler)
			require.NoError(t, err)
			assert.Equal(t, "ok", resp)
		})
	}
}

// claimsFromGatewayMetadata parses roles and returns nil when no principal.
func TestClaimsFromGatewayMetadata(t *testing.T) {
	t.Run("no metadata returns nil", func(t *testing.T) {
		assert.Nil(t, claimsFromGatewayMetadata(context.Background()))
	})

	t.Run("no user id returns nil", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs(mdKeyAuthRoles, "admin"))
		assert.Nil(t, claimsFromGatewayMetadata(ctx))
	})

	t.Run("parses user, tenant and roles", func(t *testing.T) {
		ctx := ctxWithGatewayIdentity("user-9", "admin", "operator")
		claims := claimsFromGatewayMetadata(ctx)
		require.NotNil(t, claims)
		assert.Equal(t, "user-9", claims.UserID)
		assert.Equal(t, "org_test", claims.TenantID)
		assert.Equal(t, []string{"admin", "operator"}, claims.Roles)
	})

	t.Run("trims and skips empty roles", func(t *testing.T) {
		ctx := metadata.NewIncomingContext(context.Background(),
			metadata.Pairs(mdKeyUserID, "user-1", mdKeyAuthRoles, " admin , ,operator "))
		claims := claimsFromGatewayMetadata(ctx)
		require.NotNil(t, claims)
		assert.Equal(t, []string{"admin", "operator"}, claims.Roles)
	})
}

// Stream interceptor enforces RBAC on control-plane streams and passes through
// others. ExecuteSaga is unary in proto, but the stream path is exercised here to
// guard the wiring for any future streaming control-plane RPC.
func TestGatewayManifestRBACStream(t *testing.T) {
	interceptor := GatewayManifestRBACStreamInterceptor(controlplaneservice.ManifestRBACStreamInterceptor())

	streamHandler := func(_ interface{}, _ grpc.ServerStream) error { return nil }

	t.Run("unauthenticated control-plane denied", func(t *testing.T) {
		ss := &fakeServerStream{ctx: context.Background()}
		err := interceptor(nil, ss, &grpc.StreamServerInfo{FullMethod: methodApplyManifest}, streamHandler)
		assertCode(t, err, codes.Unauthenticated)
	})

	t.Run("admin control-plane allowed", func(t *testing.T) {
		ss := &fakeServerStream{ctx: ctxWithGatewayIdentity("user-1", "admin")}
		err := interceptor(nil, ss, &grpc.StreamServerInfo{FullMethod: methodApplyManifest}, streamHandler)
		require.NoError(t, err)
	})

	t.Run("non-control-plane passthrough", func(t *testing.T) {
		ss := &fakeServerStream{ctx: context.Background()}
		err := interceptor(nil, ss, &grpc.StreamServerInfo{FullMethod: "/meridian.party.v1.PartyService/StreamParties"}, streamHandler)
		require.NoError(t, err)
	})
}

type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *fakeServerStream) Context() context.Context { return s.ctx }
