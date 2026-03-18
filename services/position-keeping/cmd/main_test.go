package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/position-keeping/app"
	"github.com/meridianhub/meridian/services/position-keeping/observability"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// mockHealthWatchServer implements grpc_health_v1.Health_WatchServer for testing
type mockHealthWatchServer struct {
	grpc_health_v1.Health_WatchServer
	ctx       context.Context
	responses []*grpc_health_v1.HealthCheckResponse
	sendErr   error
}

func (m *mockHealthWatchServer) Context() context.Context {
	return m.ctx
}

func (m *mockHealthWatchServer) Send(resp *grpc_health_v1.HealthCheckResponse) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.responses = append(m.responses, resp)
	return nil
}

func TestHealthServer_Check_WithHealthyDatabase(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// This test requires a real database connection
	// Set up minimal environment
	_ = os.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/testdb")
	_ = os.Setenv("ACCOUNT_VALIDATION_ENABLED", "false")
	defer func() {
		_ = os.Unsetenv("DATABASE_URL")
		_ = os.Unsetenv("ACCOUNT_VALIDATION_ENABLED")
	}()

	config, err := app.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError, // Quiet logging in tests
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to create container (will fail without database, which is expected in test)
	container, err := app.NewContainer(ctx, config, logger)
	if err != nil {
		// This is expected in test environment without database
		t.Skip("skipping test: database not available")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = container.Close(shutdownCtx)
	}()

	// Create health aggregator and server
	healthCheckers := []health.Checker{
		observability.NewPgxPoolChecker(container.DBPool),
	}
	if container.RedisClient != nil {
		healthCheckers = append(healthCheckers, observability.NewRedisChecker(container.RedisClient))
	}
	healthAggregator := health.NewAggregator(healthCheckers)
	healthSrv := newHealthServer(healthAggregator, logger)

	// Perform health check
	req := &grpc_health_v1.HealthCheckRequest{}
	resp, err := healthSrv.Check(ctx, req)
	if err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}

	if resp == nil {
		t.Fatal("Check() returned nil response")
		return
	}

	// With a healthy database, should return SERVING
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("Check() status = %v, want SERVING", resp.Status)
	}
}

func TestHealthServer_Check_ReturnsResponse(t *testing.T) {
	// This test doesn't require a real database - just validates structure
	_ = os.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/testdb")
	_ = os.Setenv("ACCOUNT_VALIDATION_ENABLED", "false")
	defer func() {
		_ = os.Unsetenv("DATABASE_URL")
		_ = os.Unsetenv("ACCOUNT_VALIDATION_ENABLED")
	}()

	config, err := app.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Create container (connection will likely fail, but that's ok for this test)
	container, err := app.NewContainer(ctx, config, logger)
	if err != nil {
		t.Skip("skipping test: cannot create container")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = container.Close(shutdownCtx)
	}()

	healthCheckers := []health.Checker{observability.NewPgxPoolChecker(container.DBPool)}
	if container.RedisClient != nil {
		healthCheckers = append(healthCheckers, observability.NewRedisChecker(container.RedisClient))
	}
	healthAggregator := health.NewAggregator(healthCheckers)
	healthSrv := newHealthServer(healthAggregator, logger)

	// Perform health check
	req := &grpc_health_v1.HealthCheckRequest{}
	resp, err := healthSrv.Check(ctx, req)
	// Should always return a response (even if database is unhealthy)
	if err != nil {
		t.Fatalf("Check() error = %v, want nil", err)
	}

	if resp == nil {
		t.Fatal("Check() returned nil response")
		return
	}

	// Should return either SERVING or NOT_SERVING (not unknown or other)
	if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING &&
		resp.Status != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Errorf("Check() status = %v, want SERVING or NOT_SERVING", resp.Status)
	}
}

func TestHealthServer_Watch_SendsInitialStatus(t *testing.T) {
	_ = os.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/testdb")
	_ = os.Setenv("ACCOUNT_VALIDATION_ENABLED", "false")
	defer func() {
		_ = os.Unsetenv("DATABASE_URL")
		_ = os.Unsetenv("ACCOUNT_VALIDATION_ENABLED")
	}()

	config, err := app.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	container, err := app.NewContainer(ctx, config, logger)
	if err != nil {
		t.Skip("skipping test: cannot create container")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = container.Close(shutdownCtx)
	}()

	healthCheckers := []health.Checker{observability.NewPgxPoolChecker(container.DBPool)}
	if container.RedisClient != nil {
		healthCheckers = append(healthCheckers, observability.NewRedisChecker(container.RedisClient))
	}
	healthAggregator := health.NewAggregator(healthCheckers)
	healthSrv := newHealthServer(healthAggregator, logger)

	// Create mock stream with short-lived context
	streamCtx, streamCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer streamCancel()

	mockStream := &mockHealthWatchServer{
		ctx:       streamCtx,
		responses: make([]*grpc_health_v1.HealthCheckResponse, 0),
	}

	// Start Watch in background
	done := make(chan error, 1)
	go func() {
		done <- healthSrv.Watch(&grpc_health_v1.HealthCheckRequest{}, mockStream)
	}()

	// Wait for context to expire or Watch to complete
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Watch() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		// Timeout is ok - Watch should still be running
	}

	// Verify at least initial status was sent
	if len(mockStream.responses) < 1 {
		t.Errorf("Watch() sent %d responses, want at least 1", len(mockStream.responses))
	}

	if len(mockStream.responses) > 0 {
		firstResp := mockStream.responses[0]
		if firstResp.Status != grpc_health_v1.HealthCheckResponse_SERVING &&
			firstResp.Status != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
			t.Errorf("Watch() initial status = %v, want SERVING or NOT_SERVING", firstResp.Status)
		}
	}
}

func TestHealthServer_Watch_RespectsContext(t *testing.T) {
	_ = os.Setenv("DATABASE_URL", "postgres://test:test@localhost:5432/testdb")
	_ = os.Setenv("ACCOUNT_VALIDATION_ENABLED", "false")
	defer func() {
		_ = os.Unsetenv("DATABASE_URL")
		_ = os.Unsetenv("ACCOUNT_VALIDATION_ENABLED")
	}()

	config, err := app.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelError,
	}))

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	container, err := app.NewContainer(ctx, config, logger)
	if err != nil {
		t.Skip("skipping test: cannot create container")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		_ = container.Close(shutdownCtx)
	}()

	healthCheckers := []health.Checker{observability.NewPgxPoolChecker(container.DBPool)}
	if container.RedisClient != nil {
		healthCheckers = append(healthCheckers, observability.NewRedisChecker(container.RedisClient))
	}
	healthAggregator := health.NewAggregator(healthCheckers)
	healthSrv := newHealthServer(healthAggregator, logger)

	// Create mock stream with context that we'll cancel
	streamCtx, streamCancel := context.WithCancel(context.Background())

	mockStream := &mockHealthWatchServer{
		ctx:       streamCtx,
		responses: make([]*grpc_health_v1.HealthCheckResponse, 0),
	}

	// Start Watch in background
	done := make(chan error, 1)
	go func() {
		done <- healthSrv.Watch(&grpc_health_v1.HealthCheckRequest{}, mockStream)
	}()

	// Intentional sleep: Give grpc health watch time to send initial status
	time.Sleep(50 * time.Millisecond) //nolint:forbidigo // gives gRPC health Watch time to send initial status

	// Cancel the context
	streamCancel()

	// Watch should return promptly after context cancellation
	select {
	case err := <-done:
		// Watch should return nil when context is cancelled
		if err != nil {
			t.Errorf("Watch() error = %v, want nil on context cancellation", err)
		}
	case <-time.After(1 * time.Second):
		t.Error("Watch() did not return within 1 second after context cancellation")
	}
}
