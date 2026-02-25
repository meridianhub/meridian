package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/internal-account/adapters/persistence"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// mockGRPCHealthClient implements grpc_health_v1.HealthClient for testing.
type mockGRPCHealthClient struct {
	resp *grpc_health_v1.HealthCheckResponse
	err  error
}

func (m *mockGRPCHealthClient) Check(_ context.Context, _ *grpc_health_v1.HealthCheckRequest, _ ...grpc.CallOption) (*grpc_health_v1.HealthCheckResponse, error) {
	return m.resp, m.err
}

func (m *mockGRPCHealthClient) List(_ context.Context, _ *grpc_health_v1.HealthListRequest, _ ...grpc.CallOption) (*grpc_health_v1.HealthListResponse, error) {
	return nil, errors.New("not implemented in mock")
}

func (m *mockGRPCHealthClient) Watch(_ context.Context, _ *grpc_health_v1.HealthCheckRequest, _ ...grpc.CallOption) (grpc.ServerStreamingClient[grpc_health_v1.HealthCheckResponse], error) {
	return nil, errors.New("not implemented in mock")
}

// =============================================================================
// PositionKeepingHealthChecker Tests
// =============================================================================

func TestPositionKeepingHealthChecker_Healthy(t *testing.T) {
	healthClient := &mockGRPCHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_SERVING,
		},
	}

	checker := NewPositionKeepingHealthChecker(healthClient, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "positionkeeping", result.Name)
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "positionkeeping service reachable")
	assert.NoError(t, result.Error)
}

func TestPositionKeepingHealthChecker_Unreachable(t *testing.T) {
	healthClient := &mockGRPCHealthClient{
		err: status.Error(codes.Unavailable, "connection refused"),
	}

	checker := NewPositionKeepingHealthChecker(healthClient, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "positionkeeping", result.Name)
	assert.Equal(t, health.StatusDegraded, result.Status)
	assert.Contains(t, result.Message, "positionkeeping service unreachable")
	assert.Error(t, result.Error)
}

func TestPositionKeepingHealthChecker_NotServing(t *testing.T) {
	healthClient := &mockGRPCHealthClient{
		resp: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
		},
	}

	checker := NewPositionKeepingHealthChecker(healthClient, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "positionkeeping", result.Name)
	assert.Equal(t, health.StatusDegraded, result.Status)
	assert.Contains(t, result.Message, "positionkeeping service not serving")
}

func TestPositionKeepingHealthChecker_Name(t *testing.T) {
	checker := NewPositionKeepingHealthChecker(nil, 5*time.Second)
	assert.Equal(t, "positionkeeping", checker.Name())
}

// =============================================================================
// HealthChecker Construction Tests
// =============================================================================

func TestNewHealthChecker_NilRepository(t *testing.T) {
	_, err := NewHealthChecker(HealthCheckerConfig{
		Repository: nil,
	})
	assert.ErrorIs(t, err, ErrHealthCheckerRepositoryNil)
}

func TestNewHealthChecker_IncludesPositionKeepingWhenConfigured(t *testing.T) {
	repo := persistence.NewRepository(nil)
	healthClient := &mockGRPCHealthClient{}

	hc, err := NewHealthChecker(HealthCheckerConfig{
		Repository:                  repo,
		PositionKeepingHealthClient: healthClient,
	})

	require.NoError(t, err)
	assert.NotNil(t, hc)
}

func TestNewHealthChecker_OmitsPositionKeepingWhenNotConfigured(t *testing.T) {
	repo := persistence.NewRepository(nil)

	hc, err := NewHealthChecker(HealthCheckerConfig{
		Repository:                  repo,
		PositionKeepingHealthClient: nil,
	})

	require.NoError(t, err)
	assert.NotNil(t, hc)
}

// =============================================================================
// TestHealthCheck_RequiresPositionKeeping verifies that the health check
// surfaces Position Keeping connectivity status for operational monitoring.
// =============================================================================

func TestHealthCheck_RequiresPositionKeeping_PassesWhenPKHealthy(t *testing.T) {
	pkChecker := NewPositionKeepingHealthChecker(
		&mockGRPCHealthClient{
			resp: &grpc_health_v1.HealthCheckResponse{
				Status: grpc_health_v1.HealthCheckResponse_SERVING,
			},
		},
		5*time.Second,
	)

	aggregator := health.NewAggregator([]health.Checker{pkChecker})
	report := aggregator.CheckAll(context.Background())

	assert.Equal(t, health.StatusHealthy, report.OverallStatus())
	require.Len(t, report.Components, 1)
	assert.Equal(t, "positionkeeping", report.Components[0].Name)
	assert.Equal(t, health.StatusHealthy, report.Components[0].Status)
}

func TestHealthCheck_RequiresPositionKeeping_DegradesWhenPKUnhealthy(t *testing.T) {
	pkChecker := NewPositionKeepingHealthChecker(
		&mockGRPCHealthClient{
			err: status.Error(codes.Unavailable, "position keeping connection refused"),
		},
		5*time.Second,
	)

	aggregator := health.NewAggregator([]health.Checker{pkChecker})
	report := aggregator.CheckAll(context.Background())

	assert.Equal(t, health.StatusDegraded, report.OverallStatus())
	require.Len(t, report.Components, 1)
	assert.Equal(t, "positionkeeping", report.Components[0].Name)
	assert.Equal(t, health.StatusDegraded, report.Components[0].Status)
	assert.Contains(t, report.Components[0].Message, "positionkeeping")
}

func TestHealthCheck_RequiresPositionKeeping_ErrorMentionsPositionKeeping(t *testing.T) {
	pkChecker := NewPositionKeepingHealthChecker(
		&mockGRPCHealthClient{
			err: errors.New("dial tcp: connection refused"),
		},
		5*time.Second,
	)

	result := pkChecker.Check(context.Background())

	assert.Equal(t, health.StatusDegraded, result.Status)
	assert.Contains(t, result.Message, "positionkeeping")
	assert.Error(t, result.Error)
}

// =============================================================================
// mapStatusToGRPC Tests
// =============================================================================

func TestMapStatusToGRPC(t *testing.T) {
	repo := persistence.NewRepository(nil)
	hc, err := NewHealthChecker(HealthCheckerConfig{
		Repository: repo,
	})
	require.NoError(t, err)

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
			result := hc.mapStatusToGRPC(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}
