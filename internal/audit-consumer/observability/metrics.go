// Package observability provides Prometheus metrics and health monitoring for the audit-consumer service.
// This service is deployed once per service (e.g., current-account, financial-accounting) and consumes
// audit events from a single Kafka topic, writing them to tenant-scoped audit_log tables.
//
// All metrics include a "service_name" label to distinguish between multiple deployments.
package observability

import (
	"context"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	// serviceName holds the current service name (from SERVICE_NAME environment variable).
	// Set via SetServiceName() during application initialization.
	serviceName   = "unknown"
	serviceNameMu sync.RWMutex

	// eventsProcessedTotal counts audit events successfully processed per tenant.
	// WARNING: tenant_id label has high cardinality risk. Monitor the number of unique
	// tenant_id values in production. Consider using recording rules or histograms if
	// cardinality exceeds 1000 tenants.
	eventsProcessedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "audit_consumer",
			Name:      "events_processed_total",
			Help:      "Total number of audit events successfully processed",
		},
		[]string{"service_name", "tenant_id", "operation"},
	)

	// eventsFailedTotal counts audit events that failed processing per tenant.
	// WARNING: tenant_id label has high cardinality risk. Monitor the number of unique
	// tenant_id values in production. Consider using recording rules or histograms if
	// cardinality exceeds 1000 tenants.
	eventsFailedTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "audit_consumer",
			Name:      "events_failed_total",
			Help:      "Total number of audit events that failed processing",
		},
		[]string{"service_name", "tenant_id", "operation", "reason"},
	)

	// tenantAuditWriteDuration tracks the latency of writing audit logs to tenant schemas.
	// WARNING: tenant_id label has high cardinality risk. Monitor the number of unique
	// tenant_id values in production. Consider using recording rules or histograms if
	// cardinality exceeds 1000 tenants.
	tenantAuditWriteDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "meridian",
			Subsystem: "audit_consumer",
			Name:      "tenant_audit_write_duration_seconds",
			Help:      "Duration of tenant audit write operations in seconds",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"service_name", "tenant_id"},
	)

	// consumerLag tracks the consumer lag for the single topic this deployment processes.
	// Unlike multi-topic consumers, this tracks a single topic's lag.
	consumerLag = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "audit_consumer",
			Name:      "consumer_lag_messages",
			Help:      "Number of messages the consumer is behind the latest offset",
		},
		[]string{"service_name", "topic"},
	)

	// dbConnectionPoolInUse tracks database connections currently in use.
	dbConnectionPoolInUse = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "audit_consumer",
			Name:      "db_connection_pool_in_use",
			Help:      "Number of database connections currently in use",
		},
		[]string{"service_name"},
	)

	// dbConnectionPoolIdle tracks idle database connections.
	dbConnectionPoolIdle = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "audit_consumer",
			Name:      "db_connection_pool_idle",
			Help:      "Number of idle database connections",
		},
		[]string{"service_name"},
	)

	// dbConnectionPoolWaitCount tracks total connection waits.
	dbConnectionPoolWaitCount = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "audit_consumer",
			Name:      "db_connection_pool_wait_total",
			Help:      "Total number of times waited for a database connection",
		},
		[]string{"service_name"},
	)

	// dbConnectionPoolWaitDuration tracks duration of connection waits.
	dbConnectionPoolWaitDuration = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "meridian",
			Subsystem: "audit_consumer",
			Name:      "db_connection_pool_wait_duration_seconds_total",
			Help:      "Total time spent waiting for database connections",
		},
		[]string{"service_name"},
	)

	// kafkaHealthy indicates whether Kafka connectivity is healthy (1 = healthy, 0 = unhealthy).
	kafkaHealthy = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "audit_consumer",
			Name:      "kafka_healthy",
			Help:      "Kafka connectivity health status (1 = healthy, 0 = unhealthy)",
		},
		[]string{"service_name"},
	)

	// dbHealthy indicates whether database connectivity is healthy (1 = healthy, 0 = unhealthy).
	dbHealthy = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "meridian",
			Subsystem: "audit_consumer",
			Name:      "db_healthy",
			Help:      "Database connectivity health status (1 = healthy, 0 = unhealthy)",
		},
		[]string{"service_name"},
	)
)

// SetServiceName sets the service name for all metrics labels.
// This must be called during application initialization before recording any metrics.
// Typically set from the SERVICE_NAME environment variable (e.g., "current-account", "financial-accounting").
func SetServiceName(name string) {
	if name == "" {
		name = "unknown"
	}
	serviceNameMu.Lock()
	defer serviceNameMu.Unlock()
	serviceName = name
}

// GetServiceName returns the currently configured service name.
func GetServiceName() string {
	serviceNameMu.RLock()
	defer serviceNameMu.RUnlock()
	return serviceName
}

// withServiceName executes fn with the current service name.
// This is the preferred way to access serviceName for metric recording.
func withServiceName(fn func(string)) {
	serviceNameMu.RLock()
	defer serviceNameMu.RUnlock()
	fn(serviceName)
}

// RecordEventProcessed increments the counter for successfully processed events.
// tenantID should be the tenant identifier (e.g., "org_123").
// operation should be "INSERT", "UPDATE", or "DELETE".
func RecordEventProcessed(tenantID, operation string) {
	withServiceName(func(svcName string) {
		eventsProcessedTotal.WithLabelValues(svcName, tenantID, operation).Inc()
	})
}

// RecordEventFailed increments the counter for failed event processing.
// tenantID should be the tenant identifier (e.g., "org_123").
// operation should be "INSERT", "UPDATE", or "DELETE".
// reason should describe the failure (e.g., "missing_tenant_context", "invalid_operation", "db_write_failed").
func RecordEventFailed(tenantID, operation, reason string) {
	withServiceName(func(svcName string) {
		eventsFailedTotal.WithLabelValues(svcName, tenantID, operation, reason).Inc()
	})
}

// RecordTenantAuditWriteDuration observes the duration of a tenant audit write operation.
// tenantID should be the tenant identifier (e.g., "org_123").
// duration is the time taken to write the audit log entry.
func RecordTenantAuditWriteDuration(tenantID string, duration time.Duration) {
	withServiceName(func(svcName string) {
		tenantAuditWriteDuration.WithLabelValues(svcName, tenantID).Observe(duration.Seconds())
	})
}

// RecordConsumerLag sets the current consumer lag for the topic.
// topic should be the Kafka topic name (e.g., "audit.events.current-account").
// lag is the number of messages behind the latest offset.
func RecordConsumerLag(topic string, lag float64) {
	withServiceName(func(svcName string) {
		consumerLag.WithLabelValues(svcName, topic).Set(lag)
	})
}

// RecordDBConnectionPoolStats records database connection pool metrics.
// This should be called periodically (e.g., every 10 seconds) to track pool utilization.
func RecordDBConnectionPoolStats(inUse, idle int, waitCount int64, waitDuration time.Duration) {
	withServiceName(func(svcName string) {
		dbConnectionPoolInUse.WithLabelValues(svcName).Set(float64(inUse))
		dbConnectionPoolIdle.WithLabelValues(svcName).Set(float64(idle))

		// Use Add() for counters to accumulate delta values
		// Note: This assumes we're tracking the delta since last call
		dbConnectionPoolWaitCount.WithLabelValues(svcName).Add(float64(waitCount))
		dbConnectionPoolWaitDuration.WithLabelValues(svcName).Add(waitDuration.Seconds())
	})
}

// RecordKafkaHealth sets the Kafka health status.
// healthy should be true if Kafka connectivity is working, false otherwise.
func RecordKafkaHealth(healthy bool) {
	value := 0.0
	if healthy {
		value = 1.0
	}
	withServiceName(func(svcName string) {
		kafkaHealthy.WithLabelValues(svcName).Set(value)
	})
}

// RecordDBHealth sets the database health status.
// healthy should be true if database connectivity is working, false otherwise.
func RecordDBHealth(healthy bool) {
	value := 0.0
	if healthy {
		value = 1.0
	}
	withServiceName(func(svcName string) {
		dbHealthy.WithLabelValues(svcName).Set(value)
	})
}

// HealthChecker provides health check functionality for the audit consumer.
type HealthChecker struct {
	dbPinger    DBPinger
	kafkaStatus KafkaStatusChecker
}

// DBPinger provides a Ping method for database health checks.
type DBPinger interface {
	Ping(ctx context.Context) error
}

// KafkaStatusChecker provides status information for Kafka consumer health.
type KafkaStatusChecker interface {
	IsRunning() bool
}

// NewHealthChecker creates a new health checker.
func NewHealthChecker(dbPinger DBPinger, kafkaStatus KafkaStatusChecker) *HealthChecker {
	return &HealthChecker{
		dbPinger:    dbPinger,
		kafkaStatus: kafkaStatus,
	}
}

// CheckDB checks database connectivity.
// Returns nil if healthy, error otherwise.
func (h *HealthChecker) CheckDB(ctx context.Context) error {
	return h.dbPinger.Ping(ctx)
}

// CheckKafka checks Kafka consumer status.
// Returns nil if healthy, error otherwise.
func (h *HealthChecker) CheckKafka() error {
	if !h.kafkaStatus.IsRunning() {
		return ErrKafkaNotRunning
	}
	return nil
}

// CheckAll performs all health checks.
// Returns true if all checks pass, false otherwise.
func (h *HealthChecker) CheckAll(ctx context.Context) (healthy bool, dbErr, kafkaErr error) {
	dbErr = h.CheckDB(ctx)
	kafkaErr = h.CheckKafka()

	// Update health metrics
	RecordDBHealth(dbErr == nil)
	RecordKafkaHealth(kafkaErr == nil)

	healthy = (dbErr == nil && kafkaErr == nil)
	return healthy, dbErr, kafkaErr
}

type kafkaNotRunningError struct{}

func (e kafkaNotRunningError) Error() string {
	return "kafka consumer is not running"
}

// ErrKafkaNotRunning is returned when the Kafka consumer is not running.
var ErrKafkaNotRunning = kafkaNotRunningError{}
