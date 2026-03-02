package auth

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// validatePlatformAdminClaims extracts claims from context and validates that:
// 1. Claims exist (populated by auth interceptor)
// 2. No tenant_id claim is present (platform services are tenant-agnostic)
// 3. User has platform-admin or super-admin role
func validatePlatformAdminClaims(ctx context.Context) error {
	claims, ok := ctx.Value(ClaimsContextKey).(*Claims)
	if !ok {
		return status.Error(codes.Internal, "authentication claims not found")
	}

	if claims.HasTenantID() {
		return status.Error(codes.PermissionDenied,
			"platform services do not accept tenant-scoped credentials")
	}

	if !claims.HasRole(RolePlatformAdmin.String()) && !claims.HasRole(RoleSuperAdmin.String()) {
		return status.Error(codes.PermissionDenied,
			"platform-admin or super-admin role required")
	}

	return nil
}

// PlatformAdminInterceptor validates that requests to platform-layer services
// do NOT contain tenant_id claims. Platform services are tenant-agnostic and
// should only be accessible to platform administrators.
//
// This prevents tenant users from accessing tenant management APIs.
// The interceptor should be chained AFTER the base auth interceptor which
// populates claims in context.
func PlatformAdminInterceptor() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		if err := validatePlatformAdminClaims(ctx); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// PlatformAdminStreamInterceptor is the streaming equivalent of PlatformAdminInterceptor.
func PlatformAdminStreamInterceptor() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		ss grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if err := validatePlatformAdminClaims(ss.Context()); err != nil {
			return err
		}
		return handler(srv, ss)
	}
}
