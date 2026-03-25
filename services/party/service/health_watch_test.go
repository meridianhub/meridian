package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

// mockHealthWatchStream implements grpc_health_v1.Health_WatchServer for unit testing.
type mockHealthWatchStream struct {
	ctx     context.Context
	sends   []*grpc_health_v1.HealthCheckResponse
	sendErr error
}

func (m *mockHealthWatchStream) Send(resp *grpc_health_v1.HealthCheckResponse) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.sends = append(m.sends, resp)
	return nil
}

func (m *mockHealthWatchStream) Context() context.Context     { return m.ctx }
func (m *mockHealthWatchStream) SetHeader(metadata.MD) error  { return nil }
func (m *mockHealthWatchStream) SendHeader(metadata.MD) error { return nil }
func (m *mockHealthWatchStream) SetTrailer(metadata.MD)       {}
func (m *mockHealthWatchStream) SendMsg(any) error            { return nil }
func (m *mockHealthWatchStream) RecvMsg(any) error            { return nil }

// mockHealthChecker is a simple health.Checker for testing aggregator lookups.
type mockHealthChecker struct {
	name   string
	status health.Status
}

func (m *mockHealthChecker) Name() string { return m.name }
func (m *mockHealthChecker) Check(_ context.Context) health.ComponentResult {
	return health.ComponentResult{
		Name:      m.name,
		Status:    m.status,
		Message:   "mock",
		CheckedAt: time.Now(),
	}
}

// TestHealthChecker_Check_ComponentFound tests the path where a specific
// component name is requested and found in the aggregator.
func TestHealthChecker_Check_ComponentFound(t *testing.T) {
	t.Parallel()

	checker := &mockHealthChecker{name: "database", status: health.StatusHealthy}
	aggregator := health.NewAggregator([]health.Checker{checker})

	hc := &HealthChecker{
		logger:       newTestService(newMockRepository()).logger,
		aggregator:   aggregator,
		serviceName:  "party",
		checkTimeout: 5 * time.Second,
	}

	resp, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "database",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

// TestHealthChecker_Check_ComponentFound_Unhealthy tests the path where a
// specific component is found but reports an unhealthy status.
func TestHealthChecker_Check_ComponentFound_Unhealthy(t *testing.T) {
	t.Parallel()

	checker := &mockHealthChecker{name: "database", status: health.StatusUnhealthy}
	aggregator := health.NewAggregator([]health.Checker{checker})

	hc := &HealthChecker{
		logger:       newTestService(newMockRepository()).logger,
		aggregator:   aggregator,
		serviceName:  "party",
		checkTimeout: 5 * time.Second,
	}

	resp, err := hc.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "database",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_NOT_SERVING, resp.Status)
}

// TestHealthChecker_Watch_InitialSendError tests that Watch returns an error
// when the initial health check response cannot be sent to the stream.
func TestHealthChecker_Watch_InitialSendError(t *testing.T) {
	t.Parallel()

	sendErr := errors.New("stream closed")
	stream := &mockHealthWatchStream{
		ctx:     context.Background(),
		sendErr: sendErr,
	}

	aggregator := health.NewAggregator([]health.Checker{})
	hc := &HealthChecker{
		logger:       newTestService(newMockRepository()).logger,
		aggregator:   aggregator,
		serviceName:  "party",
		checkTimeout: 5 * time.Second,
	}

	err := hc.Watch(&grpc_health_v1.HealthCheckRequest{Service: ""}, stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send initial health status")
}

// TestHealthChecker_Watch_ContextCancellation tests that Watch exits cleanly
// when the stream context is cancelled after the initial response is sent.
func TestHealthChecker_Watch_ContextCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	callCount := 0
	stream := &mockHealthWatchStream{ctx: ctx}

	aggregator := health.NewAggregator([]health.Checker{})
	hc := &HealthChecker{
		logger:       newTestService(newMockRepository()).logger,
		aggregator:   aggregator,
		serviceName:  "party",
		checkTimeout: 100 * time.Millisecond,
	}

	// Replace Send to cancel context after initial response
	origSend := stream
	_ = origSend

	// Use a custom stream that cancels context after first send
	cancellingStream := &cancelAfterFirstSendStream{
		mockHealthWatchStream: mockHealthWatchStream{ctx: ctx},
		cancel:                cancel,
		sendCount:             &callCount,
	}

	err := hc.Watch(&grpc_health_v1.HealthCheckRequest{Service: ""}, cancellingStream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "health watch context cancelled")
}

// cancelAfterFirstSendStream cancels its context after the first successful send.
type cancelAfterFirstSendStream struct {
	mockHealthWatchStream
	cancel    context.CancelFunc
	sendCount *int
}

func (c *cancelAfterFirstSendStream) Send(resp *grpc_health_v1.HealthCheckResponse) error {
	*c.sendCount++
	c.mockHealthWatchStream.sends = append(c.mockHealthWatchStream.sends, resp)
	if *c.sendCount == 1 {
		c.cancel()
	}
	return nil
}
