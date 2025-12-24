// Package observability provides Prometheus metrics and monitoring for the Tenant service.
//
// These metrics are service-local and do not use the shared MetricsCollector pattern.
// Rationale: The provisioning metrics track one-time tenant creation workflows specific
// to this service. Unlike recurring operational metrics, these are not reusable across
// services and benefit from being defined close to their usage.
//
// IMPORTANT: tenant/tenant_id labels are intentionally omitted from all metrics.
// Reason: Provisioning creates NEW tenants - each operation introduces a unique tenant
// ID, causing unbounded cardinality growth. This would lead to memory exhaustion and
// degraded query performance in Prometheus. For per-tenant provisioning details, use
// structured logging or distributed tracing (OpenTelemetry spans) instead.
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
	// Provisioning duration histogram with exponential buckets from 0.5s to ~34 minutes
	// Buckets: 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024 seconds
	// Starting at 0.5s to capture fast provisioning operations (sub-second database/schema setup)
	provisioningDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "tenant_provisioning_duration_seconds",
			Help:    "Duration of tenant provisioning operations in seconds",
			Buckets: prometheus.ExponentialBuckets(0.5, 2, 12), // 0.5, 1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024
		},
		[]string{"status"},
	)

	// Gauge for tracking number of tenants in PROVISIONING_PENDING status.
	// This reflects tenants waiting to be claimed, not in-flight provisioning work.
	// Once a tenant is claimed, it moves to PROVISIONING status and is no longer counted here.
	provisioningQueueDepth = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "tenant_provisioning_queue_depth",
			Help: "Number of tenants in PROVISIONING_PENDING status awaiting provisioning",
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

	// Counter for provisioning retry attempts aggregated across all tenants
	provisioningRetries = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "tenant_provisioning_retries_total",
			Help: "Total number of provisioning retry attempts across all tenants",
		},
	)
)

// RecordProvisioningDuration records the duration of a tenant provisioning operation.
// status should be StatusSuccess or StatusError.
func RecordProvisioningDuration(status string, duration time.Duration) {
	provisioningDuration.WithLabelValues(status).Observe(duration.Seconds())
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

// IncrementRetryAttempt increments the retry counter for tenant provisioning operations.
func IncrementRetryAttempt() {
	provisioningRetries.Inc()
}
