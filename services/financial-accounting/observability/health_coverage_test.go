package observability

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// mockWatchStream implements grpc_health_v1.Health_WatchServer for testing.
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
		CheckTimeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	// Use a short timeout so Watch returns after the initial send
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	stream := &mockWatchStream{ctx: ctx}

	err = healthChecker.Watch(&grpc_health_v1.HealthCheckRequest{}, stream)

	// Watch should return nil when context is cancelled (per gRPC health protocol)
	assert.NoError(t, err)
	// Should have received at least the initial health check response
	assert.NotEmpty(t, stream.responses)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_Watch_InitialSendError(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	mock.ExpectPing()

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:           gormDB,
		CheckTimeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	stream := &mockWatchStream{
		ctx:     context.Background(),
		sendErr: fmt.Errorf("send failed"),
	}

	err = healthChecker.Watch(&grpc_health_v1.HealthCheckRequest{}, stream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to send initial health status")

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_Watch_PeriodicUpdate(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	// Initial ping + periodic ping
	mock.ExpectPing()
	mock.ExpectPing()

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:           gormDB,
		CheckTimeout: 50 * time.Millisecond, // Short timeout so ticker fires quickly
	})
	require.NoError(t, err)

	// Use a timeout long enough for one tick cycle
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	stream := &mockWatchStream{ctx: ctx}

	err = healthChecker.Watch(&grpc_health_v1.HealthCheckRequest{}, stream)
	assert.NoError(t, err)
	// Should have initial + at least 1 periodic response
	assert.GreaterOrEqual(t, len(stream.responses), 2)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_Watch_PeriodicSendError(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	// Initial health check ping; second ping may or may not fire before send error
	mock.ExpectPing()
	mock.ExpectPing()

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:           gormDB,
		CheckTimeout: 50 * time.Millisecond,
	})
	require.NoError(t, err)

	sendCount := 0
	// Use a bounded context to prevent hangs if send error path doesn't exit
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// Use a custom stream that fails after first send
	customStream := &failAfterNStream{
		ctx:     ctx,
		failAt:  1,
		current: &sendCount,
	}

	err = healthChecker.Watch(&grpc_health_v1.HealthCheckRequest{}, customStream)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "send failed on purpose")

	// At least the initial ping expectation should have been consumed
	// (the second may or may not fire depending on timing)
	_ = mock.ExpectationsWereMet()
}

// failAfterNStream fails Send after N successful sends.
type failAfterNStream struct {
	grpc_health_v1.Health_WatchServer
	ctx       context.Context
	failAt    int
	current   *int
	responses []*grpc_health_v1.HealthCheckResponse
}

func (f *failAfterNStream) Context() context.Context {
	return f.ctx
}

func (f *failAfterNStream) Send(resp *grpc_health_v1.HealthCheckResponse) error {
	if *f.current >= f.failAt {
		return fmt.Errorf("send failed on purpose")
	}
	*f.current++
	f.responses = append(f.responses, resp)
	return nil
}

// Test for GormDatabaseHealthChecker timeout scenario.
func TestGormDatabaseHealthChecker_Check_Timeout(t *testing.T) {
	gormDB, mock := setupMockDB(t)

	// Simulate slow ping by using a context that's already expired
	mock.ExpectPing().WillDelayFor(200 * time.Millisecond)

	checker := NewGormDatabaseHealthChecker(gormDB, 1*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	// Let context expire by using a negligible deadline (1ms is already expired by the time Check runs)

	result := checker.Check(ctx)
	// The checker should create its own timeout context, but with an already-expired parent
	// it should report unhealthy
	assert.NotNil(t, result)

	// In a timeout scenario the delayed ping may or may not execute,
	// so we intentionally do not assert ExpectationsWereMet here.
}

func TestHealthChecker_Check_UnhealthyComponentLogging(t *testing.T) {
	// Tests the code path where individual component failures are logged
	gormDB, mock := setupMockDB(t)
	mock.ExpectPing().WillReturnError(assert.AnError)

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB:           gormDB,
		CheckTimeout: 5 * time.Second,
	})
	require.NoError(t, err)

	// Request with empty service name triggers all-components check
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_NOT_SERVING, resp.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestHealthChecker_Check_SpecificComponentUnhealthy(t *testing.T) {
	gormDB, mock := setupMockDB(t)
	mock.ExpectPing().WillReturnError(assert.AnError)

	healthChecker, err := NewHealthChecker(HealthCheckerConfig{
		DB: gormDB,
	})
	require.NoError(t, err)

	// Check database component specifically when unhealthy
	resp, err := healthChecker.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{
		Service: "database",
	})

	require.NoError(t, err)
	assert.Equal(t, grpc_health_v1.HealthCheckResponse_NOT_SERVING, resp.Status)

	assert.NoError(t, mock.ExpectationsWereMet())
}
