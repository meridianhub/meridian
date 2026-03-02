package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPlatformAdminInterceptor(t *testing.T) {
	t.Run("success with platform-admin role and no tenant claim", func(t *testing.T) {
		claims := &Claims{
			UserID: "admin-123",
			Roles:  []string{RolePlatformAdmin.String()},
			// No TenantID - correct for platform-layer services
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		interceptor := PlatformAdminInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("success with super-admin role and no tenant claim", func(t *testing.T) {
		claims := &Claims{
			UserID: "admin-123",
			Roles:  []string{RoleSuperAdmin.String()},
			// No TenantID - correct for platform-layer services
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		interceptor := PlatformAdminInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("rejects request with tenant claim present", func(t *testing.T) {
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank", // Has tenant claim - should be rejected
			Roles:    []string{RolePlatformAdmin.String()},
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		interceptor := PlatformAdminInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
		assert.Contains(t, st.Message(), "platform services do not accept tenant-scoped credentials")
	})

	t.Run("rejects request without platform-admin or super-admin role", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"admin", "user"}, // Has other roles but not platform-admin or super-admin
			// No TenantID
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		interceptor := PlatformAdminInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
		assert.Contains(t, st.Message(), "platform-admin or super-admin role required")
	})

	t.Run("rejects request with no roles", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{},
			// No TenantID
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		interceptor := PlatformAdminInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
		assert.Contains(t, st.Message(), "platform-admin or super-admin role required")
	})

	t.Run("error when claims missing from context", func(t *testing.T) {
		ctx := context.Background() // No claims in context

		interceptor := PlatformAdminInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
		assert.Contains(t, st.Message(), "authentication claims not found")
	})

	t.Run("rejects tenant user even with valid roles", func(t *testing.T) {
		// Scenario: A user has platform-admin role but their token was issued
		// in tenant context - this should be rejected
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{RolePlatformAdmin.String(), RoleSuperAdmin.String()}, // Both roles!
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)

		interceptor := PlatformAdminInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/InitiateTenant"}
		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.Error(t, err)
		assert.Nil(t, resp)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
		assert.Contains(t, st.Message(), "platform services do not accept tenant-scoped credentials")
	})
}

func TestPlatformAdminStreamInterceptor(t *testing.T) {
	t.Run("success with platform-admin role and no tenant claim", func(t *testing.T) {
		claims := &Claims{
			UserID: "admin-123",
			Roles:  []string{RolePlatformAdmin.String()},
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)
		stream := &mockServerStream{ctx: ctx}

		interceptor := PlatformAdminStreamInterceptor()
		info := &grpc.StreamServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/WatchTenants"}

		handler := func(_ interface{}, _ grpc.ServerStream) error {
			return nil
		}

		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)
	})

	t.Run("rejects stream with tenant claim present", func(t *testing.T) {
		claims := &Claims{
			UserID:   "user-123",
			TenantID: "acme_bank",
			Roles:    []string{RolePlatformAdmin.String()},
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)
		stream := &mockServerStream{ctx: ctx}

		interceptor := PlatformAdminStreamInterceptor()
		info := &grpc.StreamServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/WatchTenants"}

		handler := func(_ interface{}, _ grpc.ServerStream) error {
			return nil
		}

		err := interceptor(nil, stream, info, handler)

		assert.Error(t, err)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
		assert.Contains(t, st.Message(), "platform services do not accept tenant-scoped credentials")
	})

	t.Run("rejects stream without platform-admin role", func(t *testing.T) {
		claims := &Claims{
			UserID: "user-123",
			Roles:  []string{"user"},
		}
		ctx := context.WithValue(context.Background(), ClaimsContextKey, claims)
		stream := &mockServerStream{ctx: ctx}

		interceptor := PlatformAdminStreamInterceptor()
		info := &grpc.StreamServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/WatchTenants"}

		handler := func(_ interface{}, _ grpc.ServerStream) error {
			return nil
		}

		err := interceptor(nil, stream, info, handler)

		assert.Error(t, err)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
		assert.Contains(t, st.Message(), "platform-admin or super-admin role required")
	})

	t.Run("error when claims missing from stream context", func(t *testing.T) {
		ctx := context.Background()
		stream := &mockServerStream{ctx: ctx}

		interceptor := PlatformAdminStreamInterceptor()
		info := &grpc.StreamServerInfo{FullMethod: "/meridian.tenant.v1.TenantService/WatchTenants"}

		handler := func(_ interface{}, _ grpc.ServerStream) error {
			return nil
		}

		err := interceptor(nil, stream, info, handler)

		assert.Error(t, err)
		st, ok := status.FromError(err)
		assert.True(t, ok)
		assert.Equal(t, codes.Internal, st.Code())
		assert.Contains(t, st.Message(), "authentication claims not found")
	})
}
