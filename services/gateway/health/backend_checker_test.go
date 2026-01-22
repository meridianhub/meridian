package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// Test error sentinels
var (
	errNotImplemented    = errors.New("not implemented")
	errConnectionRefused = errors.New("connection refused")
)

// mockHealthClient implements grpc_health_v1.HealthClient for testing.
type mockHealthClient struct {
	response *grpc_health_v1.HealthCheckResponse
	err      error
	delay    time.Duration
}

func (m *mockHealthClient) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest, _ ...interface{}) (*grpc_health_v1.HealthCheckResponse, error) {
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return m.response, m.err
}

func (m *mockHealthClient) Watch(_ context.Context, _ *grpc_health_v1.HealthCheckRequest, _ ...interface{}) (grpc_health_v1.Health_WatchClient, error) {
	return nil, errNotImplemented
}

func TestBackendServiceChecker_Name(t *testing.T) {
	checker := NewBackendServiceChecker("party-service", nil, 5*time.Second)
	assert.Equal(t, "party-service", checker.Name())
}

func TestBackendServiceChecker_Healthy(t *testing.T) {
	mock := &mockHealthClient{
		response: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_SERVING,
		},
	}
	checker := NewBackendServiceChecker("party-service", mock, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "party-service", result.Name)
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "reachable")
	assert.Nil(t, result.Error)
	assert.True(t, result.ResponseTime > 0)
	assert.False(t, result.CheckedAt.IsZero())
}

func TestBackendServiceChecker_NotServing(t *testing.T) {
	mock := &mockHealthClient{
		response: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
		},
	}
	checker := NewBackendServiceChecker("party-service", mock, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "party-service", result.Name)
	assert.Equal(t, health.StatusDegraded, result.Status, "backend not serving should be degraded, not unhealthy")
	assert.Contains(t, result.Message, "not serving")
	assert.Nil(t, result.Error)
}

func TestBackendServiceChecker_Unreachable(t *testing.T) {
	mock := &mockHealthClient{
		err: errConnectionRefused,
	}
	checker := NewBackendServiceChecker("party-service", mock, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "party-service", result.Name)
	assert.Equal(t, health.StatusDegraded, result.Status, "backend unreachable should be degraded, not unhealthy")
	assert.Contains(t, result.Message, "unreachable")
	assert.Error(t, result.Error)
}

func TestBackendServiceChecker_Timeout(t *testing.T) {
	mock := &mockHealthClient{
		delay: 2 * time.Second, // Will exceed timeout
	}
	checker := NewBackendServiceChecker("party-service", mock, 50*time.Millisecond)

	result := checker.Check(context.Background())

	assert.Equal(t, "party-service", result.Name)
	assert.Equal(t, health.StatusDegraded, result.Status, "backend timeout should be degraded")
	assert.Contains(t, result.Message, "timeout")
	require.Error(t, result.Error)
	assert.True(t, errors.Is(result.Error, context.DeadlineExceeded))
}

func TestBackendServiceChecker_ServiceSpecific(t *testing.T) {
	mock := &mockHealthClient{
		response: &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_SERVICE_UNKNOWN,
		},
	}
	checker := NewBackendServiceChecker("current-account", mock, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "current-account", result.Name)
	assert.Equal(t, health.StatusDegraded, result.Status)
	assert.Contains(t, result.Message, "not serving")
}
