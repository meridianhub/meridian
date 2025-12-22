// Package observability provides Prometheus metrics and monitoring for the Tenant service.
package observability

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Status constants for provisioning outcomes.
const (
	StatusSuccess = "success"
	StatusError   = "error"
)

var (
	// Provisioning duration histogram with exponential buckets from 1s to ~17 minutes
	// Buckets: 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024 seconds
	provisioningDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "tenant_provisioning_duration_seconds",
			Help:    "Duration of tenant provisioning operations in seconds",
			Buckets: prometheus.ExponentialBuckets(1, 2, 11), // 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024
		},
		[]string{"tenant_id", "status"},
	)

	// Gauge for tracking number of pending tenants in provisioning queue
	provisioningQueueDepth = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "tenant_provisioning_queue_depth",
			Help: "Number of pending tenants in the provisioning queue",
		},
	)

	// Counter for service-specific provisioning failures
	serviceProvisioningFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tenant_service_provisioning_failures_total",
			Help: "Total number of service provisioning failures by service name",
		},
		[]string{"service_name"},
	)

	// Counter for provisioning retry attempts
	provisioningRetries = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "tenant_provisioning_retries_total",
			Help: "Total number of provisioning retry attempts by tenant",
		},
		[]string{"tenant_id"},
	)
)

// RecordProvisioningDuration records the duration of a tenant provisioning operation.
// tenantID should be the tenant's unique identifier.
// status should be StatusSuccess or StatusError.
func RecordProvisioningDuration(tenantID, status string, duration time.Duration) {
	provisioningDuration.WithLabelValues(tenantID, status).Observe(duration.Seconds())
}

// SetProvisioningQueueDepth sets the current depth of the provisioning queue.
// depth is the number of pending tenants waiting to be provisioned.
func SetProvisioningQueueDepth(depth int) {
	provisioningQueueDepth.Set(float64(depth))
}

// IncrementServiceFailure increments the failure counter for a specific service.
// serviceName should identify which service failed (e.g., "database", "kafka", "s3").
func IncrementServiceFailure(serviceName string) {
	serviceProvisioningFailures.WithLabelValues(serviceName).Inc()
}

// IncrementRetryAttempt increments the retry counter for a tenant provisioning operation.
// tenantID should be the tenant's unique identifier.
func IncrementRetryAttempt(tenantID string) {
	provisioningRetries.WithLabelValues(tenantID).Inc()
}
