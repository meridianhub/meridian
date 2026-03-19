package service

import (
	"context"
	"testing"

	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestMapStatusToGRPC(t *testing.T) {
	t.Parallel()

	// We need a minimal HealthChecker to call mapStatusToGRPC
	// Since NewHealthChecker panics on nil repo, create one directly
	hc := &HealthChecker{}

	tests := []struct {
		name     string
		status   health.Status
		expected grpc_health_v1.HealthCheckResponse_ServingStatus
	}{
		{"healthy maps to SERVING", health.StatusHealthy, grpc_health_v1.HealthCheckResponse_SERVING},
		{"degraded maps to SERVING", health.StatusDegraded, grpc_health_v1.HealthCheckResponse_SERVING},
		{"unhealthy maps to NOT_SERVING", health.StatusUnhealthy, grpc_health_v1.HealthCheckResponse_NOT_SERVING},
		{"unknown maps to UNKNOWN", health.StatusUnknown, grpc_health_v1.HealthCheckResponse_UNKNOWN},
		{"unrecognized maps to UNKNOWN", health.Status(99), grpc_health_v1.HealthCheckResponse_UNKNOWN},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hc.mapStatusToGRPC(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestLogHealthCheck(t *testing.T) {
	t.Parallel()

	hc := &HealthChecker{
		logger: newTestService(newMockRepository()).logger,
	}

	tests := []struct {
		name   string
		status health.Status
	}{
		{"logs healthy status", health.StatusHealthy},
		{"logs degraded status", health.StatusDegraded},
		{"logs unhealthy status", health.StatusUnhealthy},
		{"logs unknown status", health.StatusUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := &health.Report{
				Components: []health.ComponentResult{
					{Name: "database", Status: tt.status, Message: "test"},
				},
			}
			grpcStatus := hc.mapStatusToGRPC(tt.status)
			// Should not panic
			hc.logHealthCheck(report, tt.status, grpcStatus)
		})
	}

	t.Run("logs component failures for unhealthy", func(t *testing.T) {
		report := &health.Report{
			Components: []health.ComponentResult{
				{Name: "database", Status: health.StatusUnhealthy, Message: "connection refused"},
				{Name: "cache", Status: health.StatusHealthy, Message: "ok"},
			},
		}
		// Should not panic
		hc.logHealthCheck(report, health.StatusUnhealthy, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	})
}

func TestDatabaseHealthChecker_Name(t *testing.T) {
	t.Parallel()
	dhc := NewDatabaseHealthChecker(nil, 0)
	assert.Equal(t, "database", dhc.Name())
}

func TestNewHealthChecker_PanicsOnNilRepo(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() {
		NewHealthChecker(HealthCheckerConfig{
			Repository: nil,
		})
	})
}

func TestNewHealthChecker_Defaults(t *testing.T) {
	t.Parallel()

	// We need a real repository to avoid panic - but we can't use a nil one.
	// This test verifies the defaults are applied.
	// We'll use a test that just checks the config defaults logic.
	// Since NewHealthChecker requires non-nil repo, and repo is *persistence.Repository,
	// we need an actual one. Skip if can't construct without DB.
	t.Skip("requires persistence.Repository which needs DB connection")
}

func TestHealthChecker_Check_UnknownService(t *testing.T) {
	t.Parallel()

	// Create a minimal health checker using direct struct init for unit testing
	// This tests the "unknown service" path
	checkers := []health.Checker{}
	aggregator := health.NewAggregator(checkers)

	hc := &HealthChecker{
		logger:       newTestService(newMockRepository()).logger,
		aggregator:   aggregator,
		serviceName:  "party",
		checkTimeout: 0,
	}

	// Test 1: Empty service name should check all components
	resp, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "",
	})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)

	// Test 2: Matching service name
	resp, err = hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "party",
	})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)

	// Test 3: Unknown service name returns UNKNOWN
	resp, err = hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "nonexistent-service",
	})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, resp.Status)
}
