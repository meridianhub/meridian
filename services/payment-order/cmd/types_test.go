package main

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func TestParseLogLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected slog.Level
	}{
		{"debug lowercase", "debug", slog.LevelDebug},
		{"DEBUG uppercase", "DEBUG", slog.LevelDebug},
		{"Debug mixed case", "Debug", slog.LevelDebug},
		{"warn lowercase", "warn", slog.LevelWarn},
		{"warning lowercase", "warning", slog.LevelWarn},
		{"WARN uppercase", "WARN", slog.LevelWarn},
		{"WARNING uppercase", "WARNING", slog.LevelWarn},
		{"error lowercase", "error", slog.LevelError},
		{"ERROR uppercase", "ERROR", slog.LevelError},
		{"info lowercase", "info", slog.LevelInfo},
		{"INFO uppercase", "INFO", slog.LevelInfo},
		{"empty string defaults to info", "", slog.LevelInfo},
		{"unknown string defaults to info", "trace", slog.LevelInfo},
		{"random string defaults to info", "verbose", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseLogLevel(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSimpleHealthServer_Check(t *testing.T) {
	t.Parallel()

	server := &simpleHealthServer{}

	resp, err := server.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestSimpleHealthServer_Check_WithServiceName(t *testing.T) {
	t.Parallel()

	server := &simpleHealthServer{}

	resp, err := server.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "payment-order",
	})
	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestSimpleHealthServer_Watch(t *testing.T) {
	t.Parallel()

	server := &simpleHealthServer{}

	// Create a context that we can cancel to stop the Watch stream
	ctx, cancel := context.WithCancel(context.Background())

	stream := &mockHealthWatchServer{
		ctx:       ctx,
		responses: make([]*grpc_health_v1.HealthCheckResponse, 0),
	}

	// Run Watch in a goroutine since it blocks
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Watch(&grpc_health_v1.HealthCheckRequest{}, stream)
	}()

	// Cancel context to stop Watch
	cancel()

	err := <-errCh
	// Watch returns context.Canceled when context is done
	assert.ErrorIs(t, err, context.Canceled)

	// Verify at least the initial status was sent
	assert.GreaterOrEqual(t, len(stream.responses), 1)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, stream.responses[0].Status)
}

// mockHealthWatchServer implements grpc_health_v1.Health_WatchServer for testing.
type mockHealthWatchServer struct {
	grpc_health_v1.Health_WatchServer
	ctx       context.Context
	responses []*grpc_health_v1.HealthCheckResponse
}

func (m *mockHealthWatchServer) Send(resp *grpc_health_v1.HealthCheckResponse) error {
	m.responses = append(m.responses, resp)
	return nil
}

func (m *mockHealthWatchServer) Context() context.Context {
	return m.ctx
}

func TestCreateGatewayAccountConfig_NoEnvVars(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// With no env vars set, LoadGatewayAccountConfig returns ErrEmptyConfig
	_, err := createGatewayAccountConfig(logger)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gateway account config")
}

func TestCreateGatewayAccountConfig_WithEnvVars(t *testing.T) {
	// Set gateway env vars for mock gateway
	t.Setenv("GATEWAY_MOCK_ACCOUNT_ID", "acc-00000000-0000-0000-0000-000000000001")
	t.Setenv("GATEWAY_MOCK_ACCOUNT_TYPE", "NOSTRO")

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	cfg, err := createGatewayAccountConfig(logger)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Contains(t, cfg.Mappings, "mock")
}

func TestCreateRedisClient_InvalidURL(t *testing.T) {
	t.Setenv("REDIS_URL", "://invalid-url")

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	_, err := createRedisClient(logger)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid REDIS_URL")
}

func TestCreateRedisClient_ConnectionFailure(t *testing.T) {
	// Point at a non-existent Redis
	t.Setenv("REDIS_URL", "redis://127.0.0.1:19999")

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	_, err := createRedisClient(logger)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to ping Redis")
}

func TestLocalPaymentOrderClient_UpdatePaymentOrder(t *testing.T) {
	t.Parallel()

	// localPaymentOrderClient is a thin wrapper - test it with a nil service to verify it delegates
	// We can't easily pass a real service without full setup, but we can verify the wrapper compiles
	// and panics predictably with nil (showing it delegates correctly)
	client := &localPaymentOrderClient{service: nil}
	assert.Panics(t, func() {
		_, _ = client.UpdatePaymentOrder(context.Background(), nil)
	})
}
