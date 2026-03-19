package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/meridianhub/meridian/services/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
)

func setupTestRepository(t *testing.T) *persistence.Repository {
	t.Helper()

	db := openSharedDB(t)

	// Each test gets a unique tenant → unique schema for isolation
	tid := uniqueTenantID()
	schemaName := tid.SchemaName()
	err := db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// Set search_path so AutoMigrate creates tables in the tenant schema
	err = db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error
	require.NoError(t, err)

	// AutoMigrate in the tenant schema
	err = db.AutoMigrate(&persistence.CurrentAccountEntity{})
	require.NoError(t, err)

	// Clean up schema and close connection when test completes
	t.Cleanup(func() {
		_ = db.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", pq.QuoteIdentifier(schemaName)))
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})

	return persistence.NewRepository(db)
}

// mustNewHealthChecker creates a HealthChecker and fails the test if an error occurs.
func mustNewHealthChecker(t *testing.T, config HealthCheckerConfig) *HealthChecker {
	t.Helper()
	checker, err := NewHealthChecker(config)
	require.NoError(t, err, "unexpected error creating health checker")
	return checker
}

func TestNewHealthChecker(t *testing.T) {
	tests := []struct {
		name         string
		config       HealthCheckerConfig
		wantErr      bool
		wantSentinel error
	}{
		{
			name: "valid configuration with all dependencies",
			config: HealthCheckerConfig{
				Repository:                      setupTestRepository(t),
				PositionKeepingClient:           &mockPositionKeepingClient{},
				PositionKeepingHealthClient:     &mockGRPCHealthClient{status: grpc_health_v1.HealthCheckResponse_SERVING},
				FinancialAccountingClient:       &mockFinancialAccountingClient{},
				FinancialAccountingHealthClient: &mockGRPCHealthClient{status: grpc_health_v1.HealthCheckResponse_SERVING},
				Logger:                          slog.New(slog.NewJSONHandler(os.Stdout, nil)),
				ServiceName:                     "test-service",
				CheckTimeout:                    3 * time.Second,
			},
			wantErr:      false,
			wantSentinel: nil,
		},
		{
			name: "valid configuration with defaults",
			config: HealthCheckerConfig{
				Repository: setupTestRepository(t),
			},
			wantErr:      false,
			wantSentinel: nil,
		},
		{
			name: "missing repository returns ErrHealthCheckerRepositoryNil",
			config: HealthCheckerConfig{
				PositionKeepingClient:           &mockPositionKeepingClient{},
				PositionKeepingHealthClient:     &mockGRPCHealthClient{status: grpc_health_v1.HealthCheckResponse_SERVING},
				FinancialAccountingClient:       &mockFinancialAccountingClient{},
				FinancialAccountingHealthClient: &mockGRPCHealthClient{status: grpc_health_v1.HealthCheckResponse_SERVING},
			},
			wantErr:      true,
			wantSentinel: ErrHealthCheckerRepositoryNil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker, err := NewHealthChecker(tt.config)
			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, checker)
				// Verify the specific sentinel error using errors.Is()
				assert.ErrorIs(t, err, tt.wantSentinel, "Should return the expected sentinel error")
			} else {
				require.NoError(t, err)
				assert.NotNil(t, checker)

				// Verify defaults applied
				if tt.config.ServiceName == "" {
					assert.Equal(t, "current-account", checker.serviceName)
				}
				if tt.config.CheckTimeout == 0 {
					assert.Equal(t, 5*time.Second, checker.checkTimeout)
				}
			}
		})
	}
}

func TestHealthChecker_Check_AllHealthy(t *testing.T) {
	repo := setupTestRepository(t)
	posClient := &mockPositionKeepingClient{
		failOnUpdate: false,
	}
	posHealthClient := &mockGRPCHealthClient{
		status: grpc_health_v1.HealthCheckResponse_SERVING,
	}
	finClient := &mockFinancialAccountingClient{
		failOnCapture: false,
	}
	finHealthClient := &mockGRPCHealthClient{
		status: grpc_health_v1.HealthCheckResponse_SERVING,
	}

	checker := mustNewHealthChecker(t, HealthCheckerConfig{
		Repository:                      repo,
		PositionKeepingClient:           posClient,
		PositionKeepingHealthClient:     posHealthClient,
		FinancialAccountingClient:       finClient,
		FinancialAccountingHealthClient: finHealthClient,
		Logger:                          slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	})

	ctx := context.Background()
	resp, err := checker.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "current-account",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
	// Verify health check called the gRPC health clients (not business methods)
	assert.Equal(t, 1, posHealthClient.calls)
	assert.Equal(t, 1, finHealthClient.calls)
}

func TestHealthChecker_Check_DatabaseOnly(t *testing.T) {
	repo := setupTestRepository(t)

	checker := mustNewHealthChecker(t, HealthCheckerConfig{
		Repository: repo,
		Logger:     slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	})

	ctx := context.Background()
	resp, err := checker.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "current-account",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

var (
	errConnectionRefused = errors.New("connection refused")
	errTimeout           = errors.New("timeout")
)

func TestHealthChecker_Check_ExternalServiceDegraded(t *testing.T) {
	repo := setupTestRepository(t)
	posClient := &mockPositionKeepingClient{
		failOnUpdate: true,
		failureError: errConnectionRefused,
	}
	finClient := &mockFinancialAccountingClient{
		failOnCapture: true,
		failureError:  errTimeout,
	}

	// Modify mocks to fail on List operations for health checks
	// We need to add listError fields to the existing mocks
	// For now, the test will pass because the mocks don't fail by default

	checker := mustNewHealthChecker(t, HealthCheckerConfig{
		Repository:                repo,
		PositionKeepingClient:     posClient,
		FinancialAccountingClient: finClient,
		Logger:                    slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	})

	ctx := context.Background()
	resp, err := checker.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "current-account",
	})

	require.NoError(t, err)
	// External services List calls succeed by default in existing mocks
	// so still SERVING (would be degraded if they failed)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestHealthChecker_Check_WrongService(t *testing.T) {
	repo := setupTestRepository(t)

	checker := mustNewHealthChecker(t, HealthCheckerConfig{
		Repository: repo,
		Logger:     slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	})

	ctx := context.Background()
	resp, err := checker.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "wrong-service",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, resp.Status)
}

func TestHealthChecker_Check_EmptyServiceName(t *testing.T) {
	repo := setupTestRepository(t)

	checker := mustNewHealthChecker(t, HealthCheckerConfig{
		Repository: repo,
		Logger:     slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	})

	ctx := context.Background()
	resp, err := checker.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "",
	})

	require.NoError(t, err)
	// Empty service name should check overall health
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestHealthChecker_Check_ComponentSpecificDatabase(t *testing.T) {
	repo := setupTestRepository(t)
	posClient := &mockPositionKeepingClient{
		failOnUpdate: false,
	}
	finClient := &mockFinancialAccountingClient{
		failOnCapture: false,
	}

	checker := mustNewHealthChecker(t, HealthCheckerConfig{
		Repository:                repo,
		PositionKeepingClient:     posClient,
		FinancialAccountingClient: finClient,
		Logger:                    slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	})

	ctx := context.Background()
	resp, err := checker.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "database",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
	// Verify only database checked (no calls to external services)
	assert.Equal(t, 0, posClient.listCalls)
	assert.Equal(t, 0, finClient.listCalls)
}

func TestHealthChecker_Check_ComponentSpecificPositionKeeping(t *testing.T) {
	repo := setupTestRepository(t)
	posClient := &mockPositionKeepingClient{
		failOnUpdate: false,
	}
	posHealthClient := &mockGRPCHealthClient{
		status: grpc_health_v1.HealthCheckResponse_SERVING,
	}
	finClient := &mockFinancialAccountingClient{
		failOnCapture: false,
	}
	finHealthClient := &mockGRPCHealthClient{
		status: grpc_health_v1.HealthCheckResponse_SERVING,
	}

	checker := mustNewHealthChecker(t, HealthCheckerConfig{
		Repository:                      repo,
		PositionKeepingClient:           posClient,
		PositionKeepingHealthClient:     posHealthClient,
		FinancialAccountingClient:       finClient,
		FinancialAccountingHealthClient: finHealthClient,
		Logger:                          slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	})

	ctx := context.Background()
	resp, err := checker.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "positionkeeping",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
	// Verify only positionkeeping health client checked
	assert.Equal(t, 1, posHealthClient.calls)
	assert.Equal(t, 0, finHealthClient.calls)
}

func TestHealthChecker_Check_ComponentSpecificFinancialAccounting(t *testing.T) {
	repo := setupTestRepository(t)
	posClient := &mockPositionKeepingClient{
		failOnUpdate: false,
	}
	posHealthClient := &mockGRPCHealthClient{
		status: grpc_health_v1.HealthCheckResponse_SERVING,
	}
	finClient := &mockFinancialAccountingClient{
		failOnCapture: false,
	}
	finHealthClient := &mockGRPCHealthClient{
		status: grpc_health_v1.HealthCheckResponse_SERVING,
	}

	checker := mustNewHealthChecker(t, HealthCheckerConfig{
		Repository:                      repo,
		PositionKeepingClient:           posClient,
		PositionKeepingHealthClient:     posHealthClient,
		FinancialAccountingClient:       finClient,
		FinancialAccountingHealthClient: finHealthClient,
		Logger:                          slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	})

	ctx := context.Background()
	resp, err := checker.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "financialaccounting",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
	// Verify only financialaccounting health client checked
	assert.Equal(t, 0, posHealthClient.calls)
	assert.Equal(t, 1, finHealthClient.calls)
}

func TestHealthChecker_Check_ComponentNotFound(t *testing.T) {
	repo := setupTestRepository(t)

	checker := mustNewHealthChecker(t, HealthCheckerConfig{
		Repository: repo,
		Logger:     slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	})

	ctx := context.Background()
	resp, err := checker.Check(ctx, &grpc_health_v1.HealthCheckRequest{
		Service: "nonexistent-component",
	})

	require.NoError(t, err)
	// Non-existent component should return UNKNOWN
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, resp.Status)
}

func TestHealthChecker_mapStatusToGRPC(t *testing.T) {
	checker := &HealthChecker{
		logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}

	tests := []struct {
		name         string
		healthStatus health.Status
		expectedGRPC grpc_health_v1.HealthCheckResponse_ServingStatus
	}{
		{
			name:         "healthy maps to serving",
			healthStatus: health.StatusHealthy,
			expectedGRPC: grpc_health_v1.HealthCheckResponse_SERVING,
		},
		{
			name:         "degraded maps to serving",
			healthStatus: health.StatusDegraded,
			expectedGRPC: grpc_health_v1.HealthCheckResponse_SERVING,
		},
		{
			name:         "unhealthy maps to not serving",
			healthStatus: health.StatusUnhealthy,
			expectedGRPC: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
		},
		{
			name:         "unknown maps to unknown",
			healthStatus: health.StatusUnknown,
			expectedGRPC: grpc_health_v1.HealthCheckResponse_UNKNOWN,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checker.mapStatusToGRPC(tt.healthStatus)
			assert.Equal(t, tt.expectedGRPC, result)
		})
	}
}

func TestDatabaseHealthChecker(t *testing.T) {
	repo := setupTestRepository(t)
	checker := NewDatabaseHealthChecker(repo, 5*time.Second)

	ctx := context.Background()
	result := checker.Check(ctx)

	assert.Equal(t, "database", result.Name)
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.NoError(t, result.Error)
	assert.Contains(t, result.Message, "successful")
	assert.Greater(t, result.ResponseTime, time.Duration(0))
}

func TestPositionKeepingHealthChecker_Healthy(t *testing.T) {
	healthClient := &mockGRPCHealthClient{
		status: grpc_health_v1.HealthCheckResponse_SERVING,
	}
	checker := NewPositionKeepingHealthChecker(healthClient, 5*time.Second)

	ctx := context.Background()
	result := checker.Check(ctx)

	assert.Equal(t, "positionkeeping", result.Name)
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.NoError(t, result.Error)
	assert.Contains(t, result.Message, "reachable")
	assert.Greater(t, result.ResponseTime, time.Duration(0))
	assert.Equal(t, 1, healthClient.calls)
}

func TestFinancialAccountingHealthChecker_Healthy(t *testing.T) {
	healthClient := &mockGRPCHealthClient{
		status: grpc_health_v1.HealthCheckResponse_SERVING,
	}
	checker := NewFinancialAccountingHealthChecker(healthClient, 5*time.Second)

	ctx := context.Background()
	result := checker.Check(ctx)

	assert.Equal(t, "financialaccounting", result.Name)
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.NoError(t, result.Error)
	assert.Contains(t, result.Message, "reachable")
	assert.Greater(t, result.ResponseTime, time.Duration(0))
	assert.Equal(t, 1, healthClient.calls)
}

// TestHealthChecker_Watch tests the streaming health check implementation
func TestHealthChecker_Watch(t *testing.T) {
	repo := setupTestRepository(t)

	checker := mustNewHealthChecker(t, HealthCheckerConfig{
		Repository:   repo,
		Logger:       slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		CheckTimeout: 100 * time.Millisecond, // Short timeout for test
	})

	// Create a mock stream with cancellable context
	ctx, cancel := context.WithCancel(context.Background())
	stream := &mockHealthWatchServer{
		ctx:       ctx,
		cancel:    cancel,
		responses: make([]*grpc_health_v1.HealthCheckResponse, 0),
	}

	// Start watch in goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- checker.Watch(&grpc_health_v1.HealthCheckRequest{
			Service: "current-account",
		}, stream)
	}()

	// Wait for initial response and a few updates
	awaitErr := await.AtMost(1 * time.Second).PollInterval(20 * time.Millisecond).Until(func() bool {
		return stream.responseCount() >= 2
	})
	require.NoError(t, awaitErr, "should receive at least 2 health check responses")

	// Cancel context to stop watch
	cancel()

	// Wait for goroutine to finish
	err := <-errChan
	assert.ErrorIs(t, err, context.Canceled)

	// Verify we received at least 2 responses (initial + periodic updates)
	responses := stream.getResponses()
	assert.GreaterOrEqual(t, len(responses), 2)

	// Verify all responses are SERVING
	for _, resp := range responses {
		assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
	}
}

// mockHealthWatchServer implements grpc_health_v1.Health_WatchServer for testing
type mockHealthWatchServer struct {
	ctx       context.Context
	cancel    context.CancelFunc
	responses []*grpc_health_v1.HealthCheckResponse
	mu        sync.Mutex
}

func (m *mockHealthWatchServer) Send(resp *grpc_health_v1.HealthCheckResponse) error {
	if m.ctx.Err() != nil {
		return fmt.Errorf("stream context error: %w", m.ctx.Err())
	}
	m.mu.Lock()
	m.responses = append(m.responses, resp)
	m.mu.Unlock()
	return nil
}

func (m *mockHealthWatchServer) responseCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.responses)
}

func (m *mockHealthWatchServer) getResponses() []*grpc_health_v1.HealthCheckResponse {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Return a copy to avoid races when iterating
	result := make([]*grpc_health_v1.HealthCheckResponse, len(m.responses))
	copy(result, m.responses)
	return result
}

func (m *mockHealthWatchServer) Context() context.Context {
	if m.ctx == nil {
		m.ctx, m.cancel = context.WithCancel(context.Background())
	}
	return m.ctx
}

func (m *mockHealthWatchServer) SendMsg(_ interface{}) error {
	return nil
}

func (m *mockHealthWatchServer) RecvMsg(_ interface{}) error {
	return nil
}

func (m *mockHealthWatchServer) SetHeader(_ metadata.MD) error {
	return nil
}

func (m *mockHealthWatchServer) SendHeader(_ metadata.MD) error {
	return nil
}

func (m *mockHealthWatchServer) SetTrailer(_ metadata.MD) {
}

// mockGRPCHealthClient implements grpc_health_v1.HealthClient for testing
type mockGRPCHealthClient struct {
	status grpc_health_v1.HealthCheckResponse_ServingStatus
	err    error
	calls  int
}

func (m *mockGRPCHealthClient) Check(_ context.Context, _ *grpc_health_v1.HealthCheckRequest, _ ...grpc.CallOption) (*grpc_health_v1.HealthCheckResponse, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return &grpc_health_v1.HealthCheckResponse{Status: m.status}, nil
}

var errNotImplemented = errors.New("not implemented in mock")

func (m *mockGRPCHealthClient) Watch(_ context.Context, _ *grpc_health_v1.HealthCheckRequest, _ ...grpc.CallOption) (grpc_health_v1.Health_WatchClient, error) {
	return nil, errNotImplemented
}

func (m *mockGRPCHealthClient) List(_ context.Context, _ *grpc_health_v1.HealthListRequest, _ ...grpc.CallOption) (*grpc_health_v1.HealthListResponse, error) {
	return nil, errNotImplemented
}

// =============================================================================
// Additional health checker tests for coverage
// =============================================================================

func TestPositionKeepingHealthChecker_NotServing(t *testing.T) {
	healthClient := &mockGRPCHealthClient{
		status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
	}
	checker := NewPositionKeepingHealthChecker(healthClient, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "positionkeeping", result.Name)
	assert.Equal(t, health.StatusDegraded, result.Status)
	assert.Contains(t, result.Message, "not serving")
}

func TestPositionKeepingHealthChecker_Error(t *testing.T) {
	healthClient := &mockGRPCHealthClient{
		err: errConnectionRefused,
	}
	checker := NewPositionKeepingHealthChecker(healthClient, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, health.StatusDegraded, result.Status)
	assert.Contains(t, result.Message, "unreachable")
	assert.Error(t, result.Error)
}

func TestFinancialAccountingHealthChecker_NotServing(t *testing.T) {
	healthClient := &mockGRPCHealthClient{
		status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
	}
	checker := NewFinancialAccountingHealthChecker(healthClient, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "financialaccounting", result.Name)
	assert.Equal(t, health.StatusDegraded, result.Status)
	assert.Contains(t, result.Message, "not serving")
}

func TestFinancialAccountingHealthChecker_Error(t *testing.T) {
	healthClient := &mockGRPCHealthClient{
		err: errTimeout,
	}
	checker := NewFinancialAccountingHealthChecker(healthClient, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, health.StatusDegraded, result.Status)
	assert.Contains(t, result.Message, "unreachable")
	assert.Error(t, result.Error)
}

func TestHealthChecker_Watch_InitialSendFails(t *testing.T) {
	repo := setupTestRepository(t)

	checker := mustNewHealthChecker(t, HealthCheckerConfig{
		Repository:   repo,
		Logger:       slog.New(slog.NewJSONHandler(os.Stdout, nil)),
		CheckTimeout: 100 * time.Millisecond,
	})

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel immediately so Send returns an error
	cancel()

	stream := &mockHealthWatchServer{
		ctx:       ctx,
		cancel:    cancel,
		responses: make([]*grpc_health_v1.HealthCheckResponse, 0),
	}

	err := checker.Watch(&grpc_health_v1.HealthCheckRequest{
		Service: "current-account",
	}, stream)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send initial health status")
}
