package bootstrap

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ratelimit"
	"github.com/prometheus/client_golang/prometheus"
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

// TestGrpcServerBuilder_BuildFailsWithoutAuth verifies that Build() returns
// ErrAuthRequired when neither WithAuthInterceptor() nor WithoutAuth() is called.
func TestGrpcServerBuilder_BuildFailsWithoutAuth(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()

	server, err := NewGrpcServerBuilder(tracer, logger).Build()

	require.ErrorIs(t, err, ErrAuthRequired)
	assert.Nil(t, server)
}

// TestGrpcServerBuilder_WithoutAuth verifies that WithoutAuth() allows Build()
// to succeed without an auth interceptor.
func TestGrpcServerBuilder_WithoutAuth(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()

	server, err := NewGrpcServerBuilder(tracer, logger).
		WithoutAuth().
		Build()

	require.NoError(t, err)
	require.NotNil(t, server)

	info := server.GetServiceInfo()
	assert.NotNil(t, info)
}

// TestGrpcServerBuilder_WithAuth verifies that WithAuthInterceptor adds
// the auth interceptor to the chain.
func TestGrpcServerBuilder_WithAuth(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()
	authInterceptor := createTestAuthInterceptor(t)

	server, err := NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()

	require.NoError(t, err)
	require.NotNil(t, server)
}

// TestGrpcServerBuilder_WithPlatformAdmin verifies that WithPlatformAdmin
// configures the server for platform-layer services.
func TestGrpcServerBuilder_WithPlatformAdmin(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()
	authInterceptor := createTestAuthInterceptor(t)

	server, err := NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		WithPlatformAdmin().
		Build()

	require.NoError(t, err)
	require.NotNil(t, server)
}

// TestGrpcServerBuilder_WithCustomInterceptors verifies that custom interceptors
// are added to the chain.
func TestGrpcServerBuilder_WithCustomInterceptors(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()

	customUnary := func(
		ctx context.Context,
		req interface{},
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (interface{}, error) {
		return handler(ctx, req)
	}

	customStream := func(
		srv interface{},
		ss grpc.ServerStream,
		_ *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		return handler(srv, ss)
	}

	server, err := NewGrpcServerBuilder(tracer, logger).
		WithoutAuth().
		WithUnaryInterceptor(customUnary).
		WithStreamInterceptor(customStream).
		Build()

	require.NoError(t, err)
	require.NotNil(t, server)
}

// TestGrpcServerBuilder_Build verifies that Build() produces a functional server.
func TestGrpcServerBuilder_Build(t *testing.T) {
	tests := []struct {
		name          string
		authEnabled   bool
		platformAdmin bool
		authOptOut    bool
		expectErr     bool
	}{
		{
			name:       "no auth config (fail-closed)",
			authOptOut: false,
			expectErr:  true,
		},
		{
			name:       "auth opted out",
			authOptOut: true,
			expectErr:  false,
		},
		{
			name:        "with auth (tenant service)",
			authEnabled: true,
			expectErr:   false,
		},
		{
			name:          "with auth and platform admin",
			authEnabled:   true,
			platformAdmin: true,
			expectErr:     false,
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

			if tt.authOptOut {
				builder = builder.WithoutAuth()
			}

			server, err := builder.Build()

			if tt.expectErr {
				require.ErrorIs(t, err, ErrAuthRequired)
				assert.Nil(t, server)
			} else {
				require.NoError(t, err)
				require.NotNil(t, server)
				info := server.GetServiceInfo()
				assert.NotNil(t, info)
			}
		})
	}
}

// TestGrpcServerBuilder_PlatformAdminWithoutAuth verifies that WithPlatformAdmin
// without auth still requires explicit auth opt-out.
func TestGrpcServerBuilder_PlatformAdminWithoutAuth(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()

	// Platform admin without auth or opt-out should fail
	server, err := NewGrpcServerBuilder(tracer, logger).
		WithPlatformAdmin().
		Build()

	require.ErrorIs(t, err, ErrAuthRequired)
	assert.Nil(t, server)
}

// TestGrpcServerBuilder_FluentChaining verifies that all fluent methods
// return the builder for chaining.
func TestGrpcServerBuilder_FluentChaining(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()
	authInterceptor := createTestAuthInterceptor(t)

	server, err := NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		WithPlatformAdmin().
		WithUnaryInterceptor(func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
			return handler(ctx, req)
		}).
		WithStreamInterceptor(func(srv interface{}, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
			return handler(srv, ss)
		}).
		Build()

	require.NoError(t, err)
	require.NotNil(t, server)
}

// TestGrpcServerBuilder_MultipleCustomInterceptors verifies that multiple
// custom interceptors can be added.
func TestGrpcServerBuilder_MultipleCustomInterceptors(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()

	interceptor1 := func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		return handler(ctx, req)
	}
	interceptor2 := func(ctx context.Context, req interface{}, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		return handler(ctx, req)
	}

	server, err := NewGrpcServerBuilder(tracer, logger).
		WithoutAuth().
		WithUnaryInterceptor(interceptor1).
		WithUnaryInterceptor(interceptor2).
		Build()

	require.NoError(t, err)
	require.NotNil(t, server)
}

// TestGrpcServerBuilder_WithRateLimiting verifies that WithRateLimiting adds
// the rate limiter to the interceptor chain.
func TestGrpcServerBuilder_WithRateLimiting(t *testing.T) {
	tracer := testTracer(t)
	logger := testLogger()
	authInterceptor := createTestAuthInterceptor(t)

	registry := prometheus.NewRegistry()
	metrics := ratelimit.NewMetrics("test", registry)
	limiter := ratelimit.NewInterceptor(ratelimit.Config{
		BurstSize:  10,
		RefillRate: 1 * time.Minute,
	}, metrics)
	defer limiter.Stop()

	server, err := NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		WithRateLimiting(limiter).
		Build()

	require.NoError(t, err)
	require.NotNil(t, server)
}
