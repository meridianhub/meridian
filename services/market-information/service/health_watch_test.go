package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

// mockHealthWatchStream implements grpc_health_v1.Health_WatchServer for testing.
type mockHealthWatchStream struct {
	grpc_health_v1.Health_WatchServer
	ctx       context.Context
	responses []*grpc_health_v1.HealthCheckResponse
	sendErr   error
	sendCount int
}

func (m *mockHealthWatchStream) Send(resp *grpc_health_v1.HealthCheckResponse) error {
	m.sendCount++
	if m.sendErr != nil {
		return m.sendErr
	}
	m.responses = append(m.responses, resp)
	return nil
}

func (m *mockHealthWatchStream) Context() context.Context {
	return m.ctx
}

func (m *mockHealthWatchStream) SetHeader(metadata.MD) error  { return nil }
func (m *mockHealthWatchStream) SendHeader(metadata.MD) error { return nil }
func (m *mockHealthWatchStream) SetTrailer(metadata.MD)       {}
func (m *mockHealthWatchStream) SendMsg(any) error            { return nil }
func (m *mockHealthWatchStream) RecvMsg(any) error            { return nil }

func TestHealthChecker_Watch_ContextCanceled(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	checker := NewHealthChecker(HealthCheckerConfig{
		Logger:       logger,
		ServiceName:  "test-service",
		CheckTimeout: 100 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())

	stream := &mockHealthWatchStream{ctx: ctx}

	// Cancel context after a short delay to allow initial send
	go func() {
		time.Sleep(50 * time.Millisecond) //nolint:forbidigo // deliberate delay to test context cancellation
		cancel()
	}()

	err := checker.Watch(&grpc_health_v1.HealthCheckRequest{}, stream)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cancel")
	// Should have sent at least the initial response
	assert.GreaterOrEqual(t, len(stream.responses), 1)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, stream.responses[0].Status)
}

func TestHealthChecker_Watch_SendError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	checker := NewHealthChecker(HealthCheckerConfig{
		Logger:       logger,
		ServiceName:  "test-service",
		CheckTimeout: 100 * time.Millisecond,
	})

	stream := &mockHealthWatchStream{
		ctx:     context.Background(),
		sendErr: errors.New("stream broken"),
	}

	err := checker.Watch(&grpc_health_v1.HealthCheckRequest{}, stream)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send initial health status")
}

func TestHealthChecker_Watch_DefaultMapStatus(t *testing.T) {
	// Test the default branch in mapStatusToGRPC
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	checker := NewHealthChecker(HealthCheckerConfig{
		Logger:       logger,
		ServiceName:  "test-service",
		CheckTimeout: 5 * time.Second,
	})

	// Status value 99 is not a defined health.Status value
	result := checker.mapStatusToGRPC(99)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, result)
}
