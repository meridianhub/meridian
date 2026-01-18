package service

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

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
