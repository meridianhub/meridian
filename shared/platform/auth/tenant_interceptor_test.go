package auth

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// resetTenantExtractionWarning resets the sync.Once and overridable functions
// for testing the AUTH_ENABLED warning. Returns a cleanup function.
func resetTenantExtractionWarning(t *testing.T, authEnabled bool) *bytes.Buffer {
	t.Helper()

	// Save originals
	origOnce := warnOnceAuthEnabled
	origCheck := checkAuthEnabled
	origLogger := warnLogger

	// Reset
	warnOnceAuthEnabled = sync.Once{}
	checkAuthEnabled = func() bool { return authEnabled }

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	warnLogger = func() *slog.Logger { return logger }

	t.Cleanup(func() {
		warnOnceAuthEnabled = origOnce
		checkAuthEnabled = origCheck
		warnLogger = origLogger
	})

	return &buf
}

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

func TestTenantExtractionInterceptor_AuthEnabledWarning(t *testing.T) {
	t.Run("logs warning when AUTH_ENABLED is true", func(t *testing.T) {
		buf := resetTenantExtractionWarning(t, true)

		_ = TenantExtractionInterceptor()

		output := buf.String()
		assert.Contains(t, output, "TenantExtractionInterceptor is active with AUTH_ENABLED=true")
		assert.Contains(t, output, "development/testing")
	})

	t.Run("no warning when AUTH_ENABLED is false", func(t *testing.T) {
		buf := resetTenantExtractionWarning(t, false)

		_ = TenantExtractionInterceptor()

		assert.Empty(t, buf.String())
	})

	t.Run("warning fires only once across unary and stream", func(t *testing.T) {
		buf := resetTenantExtractionWarning(t, true)

		_ = TenantExtractionInterceptor()
		_ = TenantExtractionStreamInterceptor()

		// Count warning occurrences - should be exactly one
		output := buf.String()
		assert.Contains(t, output, "AUTH_ENABLED=true")
		// The sync.Once ensures only one warning line
		lines := bytes.Count(buf.Bytes(), []byte("AUTH_ENABLED=true"))
		assert.Equal(t, 1, lines, "warning should fire exactly once")
	})
}
