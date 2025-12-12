package auth

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func TestTenantExtractionInterceptor(t *testing.T) {
	t.Run("extracts tenant from metadata when not in context", func(t *testing.T) {
		md := metadata.Pairs(tenant.TenantIDKey, "acme_bank")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		interceptor := TenantExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify tenant was extracted into context
		resultCtx := resp.(context.Context)
		tenantID, ok := tenant.FromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
	})

	t.Run("is no-op when tenant already in context", func(t *testing.T) {
		// Set tenant in context directly (simulating JWT auth having set it)
		ctx := tenant.WithTenant(context.Background(), "jwt_tenant")

		// Also set a different tenant in metadata
		md := metadata.Pairs(tenant.TenantIDKey, "metadata_tenant")
		ctx = metadata.NewIncomingContext(ctx, md)

		interceptor := TenantExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify original tenant from context is preserved (JWT takes precedence)
		resultCtx := resp.(context.Context)
		tenantID, ok := tenant.FromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("jwt_tenant"), tenantID)
	})

	t.Run("context unchanged when no tenant in metadata", func(t *testing.T) {
		// No tenant in context and no tenant in metadata
		md := metadata.Pairs("other-header", "value")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		interceptor := TenantExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify no tenant in context
		resultCtx := resp.(context.Context)
		_, ok := tenant.FromContext(resultCtx)
		assert.False(t, ok)
	})

	t.Run("context unchanged when no metadata present", func(t *testing.T) {
		// No metadata at all
		ctx := context.Background()

		interceptor := TenantExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Verify no tenant in context
		resultCtx := resp.(context.Context)
		_, ok := tenant.FromContext(resultCtx)
		assert.False(t, ok)
	})

	t.Run("extracts first tenant value when multiple present", func(t *testing.T) {
		// Multiple tenant values in metadata (edge case)
		md := metadata.Pairs(
			tenant.TenantIDKey, "first_tenant",
			tenant.TenantIDKey, "second_tenant",
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		interceptor := TenantExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Should use the first value
		resultCtx := resp.(context.Context)
		tenantID, ok := tenant.FromContext(resultCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("first_tenant"), tenantID)
	})

	t.Run("ignores invalid tenant ID format in metadata", func(t *testing.T) {
		// Invalid tenant ID with special characters (validation fails)
		md := metadata.Pairs(tenant.TenantIDKey, "invalid tenant!")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		interceptor := TenantExtractionInterceptor()
		info := &grpc.UnaryServerInfo{FullMethod: "/test.Service/Method"}

		resp, err := interceptor(ctx, nil, info, mockUnaryHandler)

		assert.NoError(t, err)
		assert.NotNil(t, resp)

		// Tenant should NOT be in context due to validation failure
		resultCtx := resp.(context.Context)
		_, ok := tenant.FromContext(resultCtx)
		assert.False(t, ok)
	})
}

func TestTenantExtractionStreamInterceptor(t *testing.T) {
	t.Run("extracts tenant from metadata when not in context", func(t *testing.T) {
		md := metadata.Pairs(tenant.TenantIDKey, "acme_bank")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := TenantExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Verify tenant was extracted into context
		tenantID, ok := tenant.FromContext(capturedCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("acme_bank"), tenantID)
	})

	t.Run("is no-op when tenant already in context", func(t *testing.T) {
		// Set tenant in context directly (simulating JWT auth having set it)
		ctx := tenant.WithTenant(context.Background(), "jwt_tenant")

		// Also set a different tenant in metadata
		md := metadata.Pairs(tenant.TenantIDKey, "metadata_tenant")
		ctx = metadata.NewIncomingContext(ctx, md)

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := TenantExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Verify original tenant from context is preserved (JWT takes precedence)
		tenantID, ok := tenant.FromContext(capturedCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("jwt_tenant"), tenantID)
	})

	t.Run("context unchanged when no tenant in metadata", func(t *testing.T) {
		// No tenant in context and no tenant in metadata
		md := metadata.Pairs("other-header", "value")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := TenantExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Verify no tenant in context
		_, ok := tenant.FromContext(capturedCtx)
		assert.False(t, ok)
	})

	t.Run("context unchanged when no metadata present", func(t *testing.T) {
		// No metadata at all
		ctx := context.Background()

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := TenantExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Verify no tenant in context
		_, ok := tenant.FromContext(capturedCtx)
		assert.False(t, ok)
	})

	t.Run("extracts first tenant value when multiple present", func(t *testing.T) {
		// Multiple tenant values in metadata (edge case)
		md := metadata.Pairs(
			tenant.TenantIDKey, "first_tenant",
			tenant.TenantIDKey, "second_tenant",
		)
		ctx := metadata.NewIncomingContext(context.Background(), md)

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := TenantExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Should use the first value
		tenantID, ok := tenant.FromContext(capturedCtx)
		assert.True(t, ok)
		assert.Equal(t, tenant.TenantID("first_tenant"), tenantID)
	})

	t.Run("ignores invalid tenant ID format in metadata", func(t *testing.T) {
		// Invalid tenant ID with special characters (validation fails)
		md := metadata.Pairs(tenant.TenantIDKey, "invalid tenant!")
		ctx := metadata.NewIncomingContext(context.Background(), md)

		stream := &mockServerStream{ctx: ctx}
		info := &grpc.StreamServerInfo{FullMethod: "/test.Service/StreamMethod"}

		var capturedCtx context.Context
		handler := func(_ interface{}, ss grpc.ServerStream) error {
			capturedCtx = ss.Context()
			return nil
		}

		interceptor := TenantExtractionStreamInterceptor()
		err := interceptor(nil, stream, info, handler)

		assert.NoError(t, err)

		// Tenant should NOT be in context due to validation failure
		_, ok := tenant.FromContext(capturedCtx)
		assert.False(t, ok)
	})
}
