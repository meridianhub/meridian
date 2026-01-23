package service

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/market-information/config"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestNewHealthChecker(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	t.Run("creates health checker without database", func(t *testing.T) {
		checker := NewHealthChecker(HealthCheckerConfig{
			Logger:       logger,
			ServiceName:  "test-service",
			CheckTimeout: 5 * time.Second,
		})

		require.NotNil(t, checker)
		assert.Equal(t, "test-service", checker.serviceName)
		assert.Equal(t, 5*time.Second, checker.timeout)
	})
}

func TestHealthChecker_Check(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	t.Run("returns SERVING for empty service name", func(t *testing.T) {
		checker := NewHealthChecker(HealthCheckerConfig{
			Logger:       logger,
			ServiceName:  "test-service",
			CheckTimeout: 5 * time.Second,
		})

		resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
		require.NoError(t, err)
		assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
	})

	t.Run("returns SERVING for matching service name", func(t *testing.T) {
		checker := NewHealthChecker(HealthCheckerConfig{
			Logger:       logger,
			ServiceName:  "test-service",
			CheckTimeout: 5 * time.Second,
		})

		resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
			Service: "test-service",
		})
		require.NoError(t, err)
		assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
	})

	t.Run("returns UNKNOWN for unknown component", func(t *testing.T) {
		checker := NewHealthChecker(HealthCheckerConfig{
			Logger:       logger,
			ServiceName:  "test-service",
			CheckTimeout: 5 * time.Second,
		})

		resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
			Service: "unknown-component",
		})
		require.NoError(t, err)
		assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, resp.Status)
	})
}

func TestNewHealthChecker_WithECBEnabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Start mock ECB server
	ecbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ecbServer.Close()

	t.Run("registers ECB checker when enabled", func(t *testing.T) {
		checker := NewHealthChecker(HealthCheckerConfig{
			Logger:       logger,
			ServiceName:  "test-service",
			CheckTimeout: 5 * time.Second,
			ServiceConfig: config.Config{
				ECB: config.ECBConfig{
					Enabled:  true,
					Endpoint: ecbServer.URL,
					Timeout:  5 * time.Second,
				},
			},
		})

		require.NotNil(t, checker)

		// Check that ECB component is registered by querying for it
		resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
			Service: "ecb-api",
		})
		require.NoError(t, err)
		assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
	})

	t.Run("ECB healthy when endpoint responds 200", func(t *testing.T) {
		checker := NewHealthChecker(HealthCheckerConfig{
			Logger:       logger,
			ServiceName:  "test-service",
			CheckTimeout: 5 * time.Second,
			ServiceConfig: config.Config{
				ECB: config.ECBConfig{
					Enabled:  true,
					Endpoint: ecbServer.URL,
					Timeout:  5 * time.Second,
				},
			},
		})

		// Overall check should be SERVING
		resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
		require.NoError(t, err)
		assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
	})
}

func TestNewHealthChecker_WithECBDisabled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	t.Run("does not register ECB checker when disabled", func(t *testing.T) {
		checker := NewHealthChecker(HealthCheckerConfig{
			Logger:       logger,
			ServiceName:  "test-service",
			CheckTimeout: 5 * time.Second,
			ServiceConfig: config.Config{
				ECB: config.ECBConfig{
					Enabled: false,
				},
			},
		})

		require.NotNil(t, checker)

		// ECB component should not be registered, returning UNKNOWN
		resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
			Service: "ecb-api",
		})
		require.NoError(t, err)
		assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, resp.Status)
	})

	t.Run("still returns SERVING when ECB disabled", func(t *testing.T) {
		checker := NewHealthChecker(HealthCheckerConfig{
			Logger:       logger,
			ServiceName:  "test-service",
			CheckTimeout: 5 * time.Second,
			ServiceConfig: config.Config{
				ECB: config.ECBConfig{
					Enabled: false,
				},
			},
		})

		// Overall check should still be SERVING (no ECB dependency)
		resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
		require.NoError(t, err)
		assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
	})
}

func TestNewHealthChecker_WithECBUnhealthy(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	t.Run("returns NOT_SERVING when ECB returns 503", func(t *testing.T) {
		// Start mock ECB server that returns 503
		ecbServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer ecbServer.Close()

		checker := NewHealthChecker(HealthCheckerConfig{
			Logger:       logger,
			ServiceName:  "test-service",
			CheckTimeout: 5 * time.Second,
			ServiceConfig: config.Config{
				ECB: config.ECBConfig{
					Enabled:  true,
					Endpoint: ecbServer.URL,
					Timeout:  5 * time.Second,
				},
			},
		})

		// Overall check should be NOT_SERVING when ECB is unhealthy
		resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
		require.NoError(t, err)
		assert.Equal(t, grpc_health_v1.HealthCheckResponse_NOT_SERVING, resp.Status)
	})

	t.Run("returns NOT_SERVING when ECB unreachable", func(t *testing.T) {
		checker := NewHealthChecker(HealthCheckerConfig{
			Logger:       logger,
			ServiceName:  "test-service",
			CheckTimeout: 1 * time.Second,
			ServiceConfig: config.Config{
				ECB: config.ECBConfig{
					Enabled:  true,
					Endpoint: "http://192.0.2.1:9999", // Unreachable
					Timeout:  500 * time.Millisecond,
				},
			},
		})

		// Overall check should be NOT_SERVING when ECB is unreachable
		resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
		require.NoError(t, err)
		assert.Equal(t, grpc_health_v1.HealthCheckResponse_NOT_SERVING, resp.Status)
	})
}

func TestNewHealthChecker_UsesDefaultEndpoint(t *testing.T) {
	// This test verifies that the default ECB endpoint is used when not specified
	// We can't actually test connectivity to ECB, but we can verify the checker is created
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	t.Run("uses default ECB endpoint when not specified", func(t *testing.T) {
		checker := NewHealthChecker(HealthCheckerConfig{
			Logger:       logger,
			ServiceName:  "test-service",
			CheckTimeout: 5 * time.Second,
			ServiceConfig: config.Config{
				ECB: config.ECBConfig{
					Enabled:  true,
					Endpoint: "", // Empty = use default
					Timeout:  5 * time.Second,
				},
			},
		})

		require.NotNil(t, checker)

		// ECB component should be registered (will be unhealthy in test env but that's OK)
		resp, err := checker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
			Service: "ecb-api",
		})
		require.NoError(t, err)
		// Can be SERVING or NOT_SERVING depending on network, but should NOT be UNKNOWN
		assert.NotEqual(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, resp.Status)
	})
}

func TestHealthChecker_mapStatusToGRPC(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	checker := NewHealthChecker(HealthCheckerConfig{
		Logger:       logger,
		ServiceName:  "test-service",
		CheckTimeout: 5 * time.Second,
	})

	tests := []struct {
		name     string
		status   health.Status
		expected grpc_health_v1.HealthCheckResponse_ServingStatus
	}{
		{
			name:     "healthy maps to SERVING",
			status:   health.StatusHealthy,
			expected: grpc_health_v1.HealthCheckResponse_SERVING,
		},
		{
			name:     "degraded maps to SERVING",
			status:   health.StatusDegraded,
			expected: grpc_health_v1.HealthCheckResponse_SERVING,
		},
		{
			name:     "unhealthy maps to NOT_SERVING",
			status:   health.StatusUnhealthy,
			expected: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
		},
		{
			name:     "unknown maps to UNKNOWN",
			status:   health.StatusUnknown,
			expected: grpc_health_v1.HealthCheckResponse_UNKNOWN,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checker.mapStatusToGRPC(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}
