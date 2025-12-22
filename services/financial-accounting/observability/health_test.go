package observability

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
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

func TestHealthChecker_Check_SpecificService(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	mock.ExpectPing()

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:          gormDB,
		ServiceName: "financial-accounting",
	})
	require.NoError(t, err)

	// Check with explicit service name
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "financial-accounting",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_SERVING, resp.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_Check_SpecificComponent(t *testing.T) {
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

	assert.Equal(t, "financial-accounting", healthChecker.serviceName)
	assert.Equal(t, 5*time.Second, healthChecker.checkTimeout)
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
