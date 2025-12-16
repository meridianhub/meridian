package auth

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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
		// Extract claims from context (populated by auth interceptor)
		claims, ok := ctx.Value(ClaimsContextKey).(*Claims)
		if !ok {
			// Auth interceptor should have populated claims
			return nil, status.Error(codes.Internal, "authentication claims not found")
		}

		// Platform services MUST NOT receive tenant_id claims
		if claims.HasTenantID() {
			return nil, status.Error(codes.PermissionDenied,
				"platform services do not accept tenant-scoped credentials")
		}

		// Verify platform-admin or super-admin role
		if !claims.HasRole("platform-admin") && !claims.HasRole("super-admin") {
			return nil, status.Error(codes.PermissionDenied,
				"platform-admin or super-admin role required")
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
		ctx := ss.Context()

		// Extract claims from context (populated by auth interceptor)
		claims, ok := ctx.Value(ClaimsContextKey).(*Claims)
		if !ok {
			return status.Error(codes.Internal, "authentication claims not found")
		}

		// Platform services MUST NOT receive tenant_id claims
		if claims.HasTenantID() {
			return status.Error(codes.PermissionDenied,
				"platform services do not accept tenant-scoped credentials")
		}

		// Verify platform-admin or super-admin role
		if !claims.HasRole("platform-admin") && !claims.HasRole("super-admin") {
			return status.Error(codes.PermissionDenied,
				"platform-admin or super-admin role required")
		}

		return handler(srv, ss)
	}
}
