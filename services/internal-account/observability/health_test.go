package observability

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func setupMockDB(t *testing.T) (*gorm.DB, sqlmock.Sqlmock) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)

	// GORM pings the database on Open, so we need to expect it
	mock.ExpectPing()

	dialector := postgres.New(postgres.Config{
		Conn:       db,
		DriverName: "postgres",
	})

	gormDB, err := gorm.Open(dialector, &gorm.Config{})
	require.NoError(t, err)

	return gormDB, mock
}

// mockPositionKeepingClient implements PositionKeepingClient for testing.
type mockPositionKeepingClient struct {
	err error
}

func (m *mockPositionKeepingClient) GetAccountBalances(_ context.Context, _ *positionkeepingv1.GetAccountBalancesRequest) (*positionkeepingv1.GetAccountBalancesResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &positionkeepingv1.GetAccountBalancesResponse{}, nil
}

func (m *mockPositionKeepingClient) Close() error {
	return nil
}

func TestGormDatabaseHealthChecker_Check_Healthy(t *testing.T) {
	gormDB, mock := setupMockDB(t)

	// Expect ping to succeed
	mock.ExpectPing()

	checker := NewGormDatabaseHealthChecker(gormDB, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "database", result.Name)
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Equal(t, "database connection successful", result.Message)
	assert.Nil(t, result.Error)
	assert.NotZero(t, result.ResponseTime)
	assert.NotZero(t, result.CheckedAt)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGormDatabaseHealthChecker_Check_Unhealthy(t *testing.T) {
	gormDB, mock := setupMockDB(t)

	// Expect ping to fail
	mock.ExpectPing().WillReturnError(assert.AnError)

	checker := NewGormDatabaseHealthChecker(gormDB, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "database", result.Name)
	assert.Equal(t, health.StatusUnhealthy, result.Status)
	assert.Contains(t, result.Message, "database ping failed")
	assert.NotNil(t, result.Error)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGormDatabaseHealthChecker_Name(t *testing.T) {
	gormDB, _ := setupMockDB(t)
	checker := NewGormDatabaseHealthChecker(gormDB, 5*time.Second)

	assert.Equal(t, "database", checker.Name())
}

func TestPositionKeepingHealthChecker_Check_Healthy(t *testing.T) {
	client := &mockPositionKeepingClient{}

	checker := NewPositionKeepingHealthChecker(client, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "position-keeping", result.Name)
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "position keeping service")
	assert.Nil(t, result.Error)
	assert.NotZero(t, result.ResponseTime)
	assert.NotZero(t, result.CheckedAt)
}

func TestPositionKeepingHealthChecker_Check_ErrorIsHealthy(t *testing.T) {
	// Application-level errors (like not found) indicate the service is reachable
	client := &mockPositionKeepingClient{
		err: assert.AnError, // Using test error to simulate application-level response
	}

	checker := NewPositionKeepingHealthChecker(client, 5*time.Second)

	result := checker.Check(context.Background())

	assert.Equal(t, "position-keeping", result.Name)
	// Application errors mean the service is UP and responding
	assert.Equal(t, health.StatusHealthy, result.Status)
	assert.Contains(t, result.Message, "responded to health probe")
	assert.Nil(t, result.Error) // Error not propagated for health probe
}

func TestPositionKeepingHealthChecker_Name(t *testing.T) {
	client := &mockPositionKeepingClient{}
	checker := NewPositionKeepingHealthChecker(client, 5*time.Second)

	assert.Equal(t, "position-keeping", checker.Name())
}

func TestHealthChecker_Check_Healthy(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	mock.ExpectPing()

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:           gormDB,
		CheckTimeout: 5 * time.Second,
	})
	require.NoError(t, err)

	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_Check_Unhealthy(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	mock.ExpectPing().WillReturnError(assert.AnError)

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:           gormDB,
		CheckTimeout: 5 * time.Second,
	})
	require.NoError(t, err)

	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_NOT_SERVING, resp.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_Check_WithPositionKeeping(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	mock.ExpectPing()

	posClient := &mockPositionKeepingClient{}

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:                    gormDB,
		PositionKeepingClient: posClient,
		CheckTimeout:          5 * time.Second,
	})
	require.NoError(t, err)

	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_Check_SpecificService(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	mock.ExpectPing()

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:          gormDB,
		ServiceName: "internal-account",
	})
	require.NoError(t, err)

	// Check with explicit service name
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "internal-account",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_Check_SpecificComponent_Database(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	mock.ExpectPing()

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB: gormDB,
	})
	require.NoError(t, err)

	// Check database component specifically
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "database",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_Check_SpecificComponent_PositionKeeping(t *testing.T) {
	gormDB, _ := setupMockDB(t)
	posClient := &mockPositionKeepingClient{}

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:                    gormDB,
		PositionKeepingClient: posClient,
	})
	require.NoError(t, err)

	// Check position-keeping component specifically
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "position-keeping",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)
}

func TestHealthChecker_Check_UnknownService(t *testing.T) {
	gormDB, _ := setupMockDB(t)

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB: gormDB,
	})
	require.NoError(t, err)

	// Check unknown service
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "unknown-service",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_UNKNOWN, resp.Status)
}

func TestHealthChecker_DefaultConfig(t *testing.T) {
	gormDB, _ := setupMockDB(t)

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB: gormDB,
	})
	require.NoError(t, err)

	assert.Equal(t, "internal-account", healthChecker.serviceName)
	assert.Equal(t, DefaultHealthCheckTimeout, healthChecker.checkTimeout)
	assert.NotNil(t, healthChecker.logger)
	assert.NotNil(t, healthChecker.aggregator)
}

func TestHealthChecker_ErrorOnNilDB(t *testing.T) {
	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB: nil,
	})
	require.Error(t, err)
	assert.Nil(t, healthChecker)
	// Verify the specific sentinel error using errors.Is()
	assert.ErrorIs(t, err, ErrDatabaseNil, "Should return ErrDatabaseNil sentinel error")
}

func TestMapStatusToGRPC(t *testing.T) {
	gormDB, _ := setupMockDB(t)
	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB: gormDB,
	})
	require.NoError(t, err)

	tests := []struct {
		status   health.Status
		expected grpc_health_v1.HealthCheckResponse_ServingStatus
	}{
		{health.StatusHealthy, grpc_health_v1.HealthCheckResponse_SERVING},
		{health.StatusDegraded, grpc_health_v1.HealthCheckResponse_SERVING},
		{health.StatusUnhealthy, grpc_health_v1.HealthCheckResponse_NOT_SERVING},
		{health.StatusUnknown, grpc_health_v1.HealthCheckResponse_UNKNOWN},
	}

	for _, tt := range tests {
		result := healthChecker.mapStatusToGRPC(tt.status)
		assert.Equal(t, tt.expected, result, "status %v should map to %v", tt.status, tt.expected)
	}
}

func TestHealthChecker_CustomServiceName(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	mock.ExpectPing()

	customName := "custom-service-name"
	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:          gormDB,
		ServiceName: customName,
	})
	require.NoError(t, err)

	assert.Equal(t, customName, healthChecker.serviceName)

	// Verify health check works with custom service name
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: customName,
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_CustomTimeout(t *testing.T) {
	gormDB, _ := setupMockDB(t)

	customTimeout := 10 * time.Second
	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:           gormDB,
		CheckTimeout: customTimeout,
	})
	require.NoError(t, err)

	assert.Equal(t, customTimeout, healthChecker.checkTimeout)
}

// mockWatchStream implements grpc_health_v1.Health_WatchServer for testing Watch().
type mockWatchStream struct {
	grpc_health_v1.Health_WatchServer
	ctx       context.Context
	responses []*grpc_health_v1.HealthCheckResponse
	sendErr   error
}

func (m *mockWatchStream) Context() context.Context {
	return m.ctx
}

func (m *mockWatchStream) Send(resp *grpc_health_v1.HealthCheckResponse) error {
	if m.sendErr != nil {
		return m.sendErr
	}
	m.responses = append(m.responses, resp)
	return nil
}

func TestHealthChecker_Watch_SendsInitialAndCancels(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	// Expect ping for the initial health check
	mock.ExpectPing()

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:           gormDB,
		CheckTimeout: 1 * time.Second,
	})
	require.NoError(t, err)

	// Create a context that cancels immediately after initial send
	ctx, cancel := context.WithCancel(context.Background())

	stream := &mockWatchStream{ctx: ctx}

	// Cancel after a short delay to allow initial send but stop the loop
	go func() {
		// Wait for initial response to be sent
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err = healthChecker.Watch(&grpc_health_v1.HealthCheckRequest{}, stream)
	assert.NoError(t, err)

	// Should have received at least the initial response
	require.GreaterOrEqual(t, len(stream.responses), 1)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, stream.responses[0].Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_Watch_SendError(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	mock.ExpectPing()

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:           gormDB,
		CheckTimeout: 1 * time.Second,
	})
	require.NoError(t, err)

	stream := &mockWatchStream{
		ctx:     context.Background(),
		sendErr: assert.AnError,
	}

	err = healthChecker.Watch(&grpc_health_v1.HealthCheckRequest{}, stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send initial health status")

	assert.NoError(t, mock.ExpectationsWereMet())
}
