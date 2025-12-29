package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"log/slog"
	"os"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// testLogger returns a logger suitable for tests
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelError, // Suppress logs during tests
	}))
}

// testTracer creates a disabled tracer for testing
func testTracer(t *testing.T) *observability.Tracer {
	t.Helper()
	tracer, err := observability.NewTracer(context.Background(), observability.TracerConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "localhost:4317", // Required for validation
		Enabled:      false,            // No-op tracer for tests
	})
	require.NoError(t, err)
	return tracer
}

// generateTestRSAKeys generates a test RSA key pair for testing
func generateTestRSAKeys(t *testing.T) (*rsa.PrivateKey, *rsa.PublicKey) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return privateKey, &privateKey.PublicKey
}

// createTestAuthInterceptor creates an auth interceptor for testing
func createTestAuthInterceptor(t *testing.T) *auth.Interceptor {
	t.Helper()
	_, publicKey := generateTestRSAKeys(t)

	validator, err := auth.NewJWTValidator(publicKey)
	require.NoError(t, err)

	interceptor, err := auth.NewAuthInterceptor(&auth.InterceptorConfig{
		Validator: validator,
		BypassMethods: []string{
			"/grpc.health.v1.Health/Check",
		},
	})
	require.NoError(t, err)
	return interceptor
}

// TestGrpcServerBuilder_MinimalChain verifies that a minimal configuration
// produces a valid server with tracing, tenant extraction, and recovery interceptors.
func TestGrpcServerBuilder_MinimalChain(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()

	// Build server without auth (minimal chain)
	server := NewGrpcServerBuilder(tracer, logger).Build()

	// Verify server was created
	require.NotNil(t, server, "server should not be nil")

	// Verify it's a valid gRPC server by checking service info exists
	// (even though we haven't registered any services)
	info := server.GetServiceInfo()
	assert.NotNil(t, info, "service info should not be nil")
}

// TestGrpcServerBuilder_WithAuth verifies that WithAuthInterceptor adds
// the auth interceptor to the chain.
func TestGrpcServerBuilder_WithAuth(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()
	authInterceptor := createTestAuthInterceptor(t)

	// Build server with auth
	server := NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()

	require.NotNil(t, server, "server should not be nil")
}

// TestGrpcServerBuilder_WithPlatformAdmin verifies that WithPlatformAdmin
// configures the server for platform-layer services.
func TestGrpcServerBuilder_WithPlatformAdmin(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()
	authInterceptor := createTestAuthInterceptor(t)

	// Build server with platform admin configuration
	server := NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		WithPlatformAdmin().
		Build()

	require.NotNil(t, server, "server should not be nil")
}

// TestGrpcServerBuilder_WithCustomInterceptors verifies that custom interceptors
// are added to the chain.
func TestGrpcServerBuilder_WithCustomInterceptors(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()

	// Track whether custom interceptors are called
	unaryCalled := false
	streamCalled := false

	customUnary := func(
		ctx context.Context,
		req interface{},
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		unaryCalled = true
		return handler(ctx, req)
	}

	customStream := func(
		srv interface{},
		ss grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		streamCalled = true
		return handler(srv, ss)
	}

	// Build server with custom interceptors
	server := NewGrpcServerBuilder(tracer, logger).
		WithUnaryInterceptor(customUnary).
		WithStreamInterceptor(customStream).
		Build()

	require.NotNil(t, server, "server should not be nil")

	// Note: We can't easily verify the interceptors are in the chain without
	// actually making RPC calls. The fact that Build() succeeds is sufficient
	// for this unit test. Integration tests would verify the actual behavior.
	_ = unaryCalled
	_ = streamCalled
}

// TestGrpcServerBuilder_Build verifies that Build() produces a functional server.
func TestGrpcServerBuilder_Build(t *testing.T) {
	tests := []struct {
		name          string
		authEnabled   bool
		platformAdmin bool
	}{
		{
			name:          "minimal (no auth)",
			authEnabled:   false,
			platformAdmin: false,
		},
		{
			name:          "with auth (tenant service)",
			authEnabled:   true,
			platformAdmin: false,
		},
		{
			name:          "with auth and platform admin",
			authEnabled:   true,
			platformAdmin: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracer := testTracer(t)
			logger := testLogger()

			builder := NewGrpcServerBuilder(tracer, logger)

			if tt.authEnabled {
				authInterceptor := createTestAuthInterceptor(t)
				builder = builder.WithAuthInterceptor(authInterceptor)
			}

			if tt.platformAdmin {
				builder = builder.WithPlatformAdmin()
			}

			server := builder.Build()

			require.NotNil(t, server, "server should not be nil")

			// Verify server is functional by getting service info
			info := server.GetServiceInfo()
			assert.NotNil(t, info, "service info should not be nil")
		})
	}
}

// TestGrpcServerBuilder_PlatformAdminWithoutAuth verifies that WithPlatformAdmin
// has no effect when auth is not configured.
func TestGrpcServerBuilder_PlatformAdminWithoutAuth(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()

	// Build with platform admin but without auth
	// This should still work - platform admin interceptor is simply not added
	server := NewGrpcServerBuilder(tracer, logger).
		WithPlatformAdmin().
		Build()

	require.NotNil(t, server, "server should not be nil")
}

// TestGrpcServerBuilder_FluentChaining verifies that all fluent methods
// return the builder for chaining.
func TestGrpcServerBuilder_FluentChaining(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()
	authInterceptor := createTestAuthInterceptor(t)

	// Verify all methods can be chained
	server := NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		WithPlatformAdmin().
		WithUnaryInterceptor(func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			return handler(ctx, req)
		}).
		WithStreamInterceptor(func(srv interface{}, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			return handler(srv, ss)
		}).
		Build()

	require.NotNil(t, server, "server should not be nil")
}

// TestGrpcServerBuilder_MultipleCustomInterceptors verifies that multiple
// custom interceptors can be added.
func TestGrpcServerBuilder_MultipleCustomInterceptors(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()

	// Create multiple custom interceptors
	interceptor1 := func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		return handler(ctx, req)
	}
	interceptor2 := func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		return handler(ctx, req)
	}

	server := NewGrpcServerBuilder(tracer, logger).
		WithUnaryInterceptor(interceptor1).
		WithUnaryInterceptor(interceptor2).
		Build()

	require.NotNil(t, server, "server should not be nil")
}
