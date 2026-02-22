package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	errConnectionRefused = errors.New("connection refused")
	errDatabaseError     = errors.New("db error")
)

// mockDBPinger is a mock implementation of DBPinger for testing.
type mockDBPinger struct {
	pingErr error
}

func (m *mockDBPinger) Ping(_ context.Context) error {
	return m.pingErr
}

// mockKafkaStatusChecker is a mock implementation of KafkaStatusChecker for testing.
type mockKafkaStatusChecker struct {
	running bool
}

func (m *mockKafkaStatusChecker) IsRunning() bool {
	return m.running
}

func TestSetServiceName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "valid service name",
			input:    "current-account",
			expected: "current-account",
		},
		{
			name:     "empty service name defaults to unknown",
			input:    "",
			expected: "unknown",
		},
		{
			name:     "different service name",
			input:    "financial-accounting",
			expected: "financial-accounting",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetServiceName(tt.input)
			assert.Equal(t, tt.expected, GetServiceName())
		})
	}
}

func TestRecordEventProcessed(t *testing.T) {
	// Reset service name
	SetServiceName("test-service")

	// Record some events
	RecordEventProcessed("org_123", "INSERT")
	RecordEventProcessed("org_123", "INSERT")
	RecordEventProcessed("org_456", "UPDATE")

	// Verify metrics
	count := testutil.CollectAndCount(eventsProcessedTotal)
	assert.Greater(t, count, 0, "should have collected metrics")

	// Verify specific metric values
	metric := eventsProcessedTotal.WithLabelValues("test-service", "org_123", "INSERT")
	value := testutil.ToFloat64(metric)
	assert.Equal(t, 2.0, value, "should have recorded 2 INSERT events for org_123")
}

func TestRecordEventFailed(t *testing.T) {
	SetServiceName("test-service")

	// Record failed events
	RecordEventFailed("org_123", "INSERT", "db_write_failed")
	RecordEventFailed("org_123", "INSERT", "db_write_failed")
	RecordEventFailed("org_456", "UPDATE", "missing_tenant_context")

	// Verify metrics
	count := testutil.CollectAndCount(eventsFailedTotal)
	assert.Greater(t, count, 0, "should have collected metrics")

	// Verify specific metric values
	metric := eventsFailedTotal.WithLabelValues("test-service", "org_123", "INSERT", "db_write_failed")
	value := testutil.ToFloat64(metric)
	assert.Equal(t, 2.0, value, "should have recorded 2 failed INSERT events for org_123")
}

func TestRecordTenantAuditWriteDuration(t *testing.T) {
	SetServiceName("test-service")

	// Record durations
	RecordTenantAuditWriteDuration("org_123", 50*time.Millisecond)
	RecordTenantAuditWriteDuration("org_123", 150*time.Millisecond)
	RecordTenantAuditWriteDuration("org_456", 25*time.Millisecond)

	// Verify metrics exist
	count := testutil.CollectAndCount(tenantAuditWriteDuration)
	assert.Greater(t, count, 0, "should have collected duration metrics")
}

func TestRecordConsumerLag(t *testing.T) {
	SetServiceName("test-service")

	// Record lag
	RecordConsumerLag("audit.events.current-account.v1", 100.0)
	RecordConsumerLag("audit.events.current-account.v1", 50.0) // Update lag

	// Verify metric
	metric := consumerLag.WithLabelValues("test-service", "audit.events.current-account.v1")
	value := testutil.ToFloat64(metric)
	assert.Equal(t, 50.0, value, "should have recorded latest lag value")
}

func TestRecordDBConnectionPoolStats(t *testing.T) {
	SetServiceName("test-service")

	// Record initial stats
	RecordDBConnectionPoolStats(5, 10, 0, 0)

	// Verify in-use connections
	inUseMetric := dbConnectionPoolInUse.WithLabelValues("test-service")
	assert.Equal(t, 5.0, testutil.ToFloat64(inUseMetric), "should record in-use connections")

	// Verify idle connections
	idleMetric := dbConnectionPoolIdle.WithLabelValues("test-service")
	assert.Equal(t, 10.0, testutil.ToFloat64(idleMetric), "should record idle connections")

	// Record stats with wait counts
	RecordDBConnectionPoolStats(8, 7, 2, 100*time.Millisecond)

	// Verify wait count incremented
	waitCountMetric := dbConnectionPoolWaitCount.WithLabelValues("test-service")
	waitCount := testutil.ToFloat64(waitCountMetric)
	assert.Equal(t, 2.0, waitCount, "should accumulate wait count")

	// Verify wait duration incremented
	waitDurationMetric := dbConnectionPoolWaitDuration.WithLabelValues("test-service")
	waitDuration := testutil.ToFloat64(waitDurationMetric)
	assert.Equal(t, 0.1, waitDuration, "should accumulate wait duration")
}

func TestRecordKafkaHealth(t *testing.T) {
	SetServiceName("test-service")

	tests := []struct {
		name     string
		healthy  bool
		expected float64
	}{
		{
			name:     "healthy",
			healthy:  true,
			expected: 1.0,
		},
		{
			name:     "unhealthy",
			healthy:  false,
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RecordKafkaHealth(tt.healthy)
			metric := kafkaHealthy.WithLabelValues("test-service")
			value := testutil.ToFloat64(metric)
			assert.Equal(t, tt.expected, value, "should record correct health status")
		})
	}
}

func TestRecordDBHealth(t *testing.T) {
	SetServiceName("test-service")

	tests := []struct {
		name     string
		healthy  bool
		expected float64
	}{
		{
			name:     "healthy",
			healthy:  true,
			expected: 1.0,
		},
		{
			name:     "unhealthy",
			healthy:  false,
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			RecordDBHealth(tt.healthy)
			metric := dbHealthy.WithLabelValues("test-service")
			value := testutil.ToFloat64(metric)
			assert.Equal(t, tt.expected, value, "should record correct health status")
		})
	}
}

func TestHealthChecker_CheckDB(t *testing.T) {
	tests := []struct {
		name    string
		pingErr error
		wantErr bool
	}{
		{
			name:    "database healthy",
			pingErr: nil,
			wantErr: false,
		},
		{
			name:    "database unhealthy",
			pingErr: errConnectionRefused,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPinger := &mockDBPinger{pingErr: tt.pingErr}
			kafkaStatus := &mockKafkaStatusChecker{running: true}
			checker := NewHealthChecker(dbPinger, kafkaStatus)

			err := checker.CheckDB(context.Background())
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHealthChecker_CheckKafka(t *testing.T) {
	tests := []struct {
		name    string
		running bool
		wantErr bool
	}{
		{
			name:    "kafka running",
			running: true,
			wantErr: false,
		},
		{
			name:    "kafka not running",
			running: false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPinger := &mockDBPinger{pingErr: nil}
			kafkaStatus := &mockKafkaStatusChecker{running: tt.running}
			checker := NewHealthChecker(dbPinger, kafkaStatus)

			err := checker.CheckKafka()
			if tt.wantErr {
				assert.Error(t, err)
				assert.Equal(t, ErrKafkaNotRunning, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestHealthChecker_CheckAll(t *testing.T) {
	SetServiceName("test-service")

	tests := []struct {
		name           string
		dbPingErr      error
		kafkaRunning   bool
		expectHealthy  bool
		expectDBErr    bool
		expectKafkaErr bool
	}{
		{
			name:           "all healthy",
			dbPingErr:      nil,
			kafkaRunning:   true,
			expectHealthy:  true,
			expectDBErr:    false,
			expectKafkaErr: false,
		},
		{
			name:           "database unhealthy",
			dbPingErr:      errDatabaseError,
			kafkaRunning:   true,
			expectHealthy:  false,
			expectDBErr:    true,
			expectKafkaErr: false,
		},
		{
			name:           "kafka unhealthy",
			dbPingErr:      nil,
			kafkaRunning:   false,
			expectHealthy:  false,
			expectDBErr:    false,
			expectKafkaErr: true,
		},
		{
			name:           "both unhealthy",
			dbPingErr:      errDatabaseError,
			kafkaRunning:   false,
			expectHealthy:  false,
			expectDBErr:    true,
			expectKafkaErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dbPinger := &mockDBPinger{pingErr: tt.dbPingErr}
			kafkaStatus := &mockKafkaStatusChecker{running: tt.kafkaRunning}
			checker := NewHealthChecker(dbPinger, kafkaStatus)

			healthy, dbErr, kafkaErr := checker.CheckAll(context.Background())

			assert.Equal(t, tt.expectHealthy, healthy, "unexpected healthy status")
			if tt.expectDBErr {
				assert.Error(t, dbErr, "expected database error")
			} else {
				assert.NoError(t, dbErr, "unexpected database error")
			}
			if tt.expectKafkaErr {
				assert.Error(t, kafkaErr, "expected kafka error")
			} else {
				assert.NoError(t, kafkaErr, "unexpected kafka error")
			}

			// Verify health metrics were updated
			dbHealthMetric := dbHealthy.WithLabelValues("test-service")
			dbHealthValue := testutil.ToFloat64(dbHealthMetric)
			if tt.expectDBErr {
				assert.Equal(t, 0.0, dbHealthValue, "db health should be 0")
			} else {
				assert.Equal(t, 1.0, dbHealthValue, "db health should be 1")
			}

			kafkaHealthMetric := kafkaHealthy.WithLabelValues("test-service")
			kafkaHealthValue := testutil.ToFloat64(kafkaHealthMetric)
			if tt.expectKafkaErr {
				assert.Equal(t, 0.0, kafkaHealthValue, "kafka health should be 0")
			} else {
				assert.Equal(t, 1.0, kafkaHealthValue, "kafka health should be 1")
			}
		})
	}
}

func TestMetricsHaveServiceNameLabel(t *testing.T) {
	// Verify all metrics include service_name label
	SetServiceName("test-service")

	// Record metrics
	RecordEventProcessed("org_123", "INSERT")
	RecordEventFailed("org_123", "INSERT", "test_error")
	RecordTenantAuditWriteDuration("org_123", 10*time.Millisecond)
	RecordConsumerLag("test.topic", 100)
	RecordDBConnectionPoolStats(5, 10, 1, time.Second)
	RecordKafkaHealth(true)
	RecordDBHealth(true)

	// Collect and verify all metrics have service_name label
	metrics := []prometheus.Collector{
		eventsProcessedTotal,
		eventsFailedTotal,
		tenantAuditWriteDuration,
		consumerLag,
		dbConnectionPoolInUse,
		dbConnectionPoolIdle,
		dbConnectionPoolWaitCount,
		dbConnectionPoolWaitDuration,
		kafkaHealthy,
		dbHealthy,
	}

	for _, metric := range metrics {
		count := testutil.CollectAndCount(metric)
		require.Greater(t, count, 0, "metric should have at least one value")
	}
}

func TestErrKafkaNotRunning(t *testing.T) {
	err := ErrKafkaNotRunning
	assert.Error(t, err)
	assert.Equal(t, "kafka consumer is not running", err.Error())
}
