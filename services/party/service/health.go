// Package service implements gRPC services for the party reference data domain
package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// HealthChecker implements gRPC health check protocol for the Party service.
// It checks the health of critical dependencies (database).
type HealthChecker struct {
	grpc_health_v1.UnimplementedHealthServer
	repo         *persistence.Repository
	logger       *slog.Logger
	aggregator   *health.Aggregator
	serviceName  string
	checkTimeout time.Duration
}

// HealthCheckerConfig contains configuration for creating a new HealthChecker.
type HealthCheckerConfig struct {
	Repository   *persistence.Repository
	Logger       *slog.Logger
	ServiceName  string        // Defaults to "party"
	CheckTimeout time.Duration // Defaults to 5 seconds
}

// NewHealthChecker creates a new health checker with dependency checking.
//
// The health checker evaluates:
// - Database connectivity (critical)
//
// Health states:
// - SERVING: Database is healthy
// - NOT_SERVING: Database unreachable
// - UNKNOWN: Unable to determine health
func NewHealthChecker(config HealthCheckerConfig) *HealthChecker {
	if config.Repository == nil {
		panic("health checker requires non-nil repository")
	}

	// Apply defaults
	if config.ServiceName == "" {
		config.ServiceName = "party"
	}
	if config.CheckTimeout == 0 {
		config.CheckTimeout = 5 * time.Second
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	// Build list of health checkers
	checkers := []health.Checker{
		NewDatabaseHealthChecker(config.Repository, config.CheckTimeout),
	}

	aggregator := health.NewAggregator(checkers)

	return &HealthChecker{
		repo:         config.Repository,
		logger:       config.Logger,
		aggregator:   aggregator,
		serviceName:  config.ServiceName,
		checkTimeout: config.CheckTimeout,
	}
}

// Check implements gRPC health check protocol.
// It performs synchronous health checks on all dependencies and returns the overall status.
func (h *HealthChecker) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	// Apply timeout to health check context
	checkCtx, cancel := context.WithTimeout(ctx, h.checkTimeout)
	defer cancel()

	// Empty service name or matching service name: check all components
	if req.Service == "" || req.Service == h.serviceName {
		// Perform health check on all components
		report := h.aggregator.CheckAll(checkCtx)
		overallStatus := report.OverallStatus()

		// Map health status to gRPC health check status
		grpcStatus := h.mapStatusToGRPC(overallStatus)

		// Log health check result
		h.logHealthCheck(report, overallStatus, grpcStatus)

		return &grpc_health_v1.HealthCheckResponse{
			Status: grpcStatus,
		}, nil
	}

	// Check if request is for a specific component
	componentResult, found := h.aggregator.CheckByName(checkCtx, req.Service)
	if found {
		// Component found - return its specific health status
		grpcStatus := h.mapStatusToGRPC(componentResult.Status)

		// Log component health check
		h.logger.Info("component health check completed",
			"component", componentResult.Name,
			"status", componentResult.Status.String(),
			"grpc_status", grpcStatus.String(),
			"response_time_ms", componentResult.ResponseTime.Milliseconds(),
			"message", componentResult.Message)

		return &grpc_health_v1.HealthCheckResponse{
			Status: grpcStatus,
		}, nil
	}

	// Service name doesn't match and component not found - return UNKNOWN
	h.logger.Debug("health check for unknown service or component",
		"requested", req.Service,
		"service_name", h.serviceName)
	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_UNKNOWN,
	}, nil
}

// Watch implements streaming health checks.
// It sends periodic health updates to the client over a stream.
func (h *HealthChecker) Watch(req *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	ctx := stream.Context()

	// Send immediate health check
	resp, err := h.Check(ctx, req)
	if err != nil {
		return err
	}

	if err := stream.Send(resp); err != nil {
		h.logger.Error("failed to send initial health check",
			"error", err)
		return fmt.Errorf("failed to send initial health status: %w", err)
	}

	// Stream periodic updates
	ticker := time.NewTicker(h.checkTimeout)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.logger.Debug("health watch stream closed", "reason", ctx.Err())
			return fmt.Errorf("health watch context cancelled: %w", ctx.Err())

		case <-ticker.C:
			resp, err := h.Check(ctx, req)
			if err != nil {
				h.logger.Error("health check failed during watch",
					"error", err)
				return err
			}

			if err := stream.Send(resp); err != nil {
				h.logger.Error("failed to send health check update",
					"error", err)
				return fmt.Errorf("failed to send health status update: %w", err)
			}
		}
	}
}

// mapStatusToGRPC converts internal health status to gRPC health check status.
func (h *HealthChecker) mapStatusToGRPC(status health.Status) grpc_health_v1.HealthCheckResponse_ServingStatus {
	switch status {
	case health.StatusHealthy:
		return grpc_health_v1.HealthCheckResponse_SERVING

	case health.StatusDegraded:
		// Degraded still serves requests
		return grpc_health_v1.HealthCheckResponse_SERVING

	case health.StatusUnhealthy:
		return grpc_health_v1.HealthCheckResponse_NOT_SERVING

	case health.StatusUnknown:
		return grpc_health_v1.HealthCheckResponse_UNKNOWN

	default:
		return grpc_health_v1.HealthCheckResponse_UNKNOWN
	}
}

// logHealthCheck logs the health check result with structured details.
func (h *HealthChecker) logHealthCheck(report *health.Report, overallStatus health.Status, grpcStatus grpc_health_v1.HealthCheckResponse_ServingStatus) {
	// Build component status map for structured logging
	componentStatuses := make(map[string]string)
	for _, comp := range report.Components {
		componentStatuses[comp.Name] = comp.Status.String()
	}

	// Log at appropriate level based on status
	switch overallStatus {
	case health.StatusHealthy, health.StatusDegraded:
		h.logger.Info("health check completed",
			"overall_status", overallStatus.String(),
			"grpc_status", grpcStatus.String(),
			"component_count", len(report.Components),
			"components", componentStatuses)

	case health.StatusUnhealthy, health.StatusUnknown:
		h.logger.Warn("health check detected issues",
			"overall_status", overallStatus.String(),
			"grpc_status", grpcStatus.String(),
			"component_count", len(report.Components),
			"components", componentStatuses)

		// Log individual component failures
		for _, comp := range report.Components {
			if comp.Status == health.StatusUnhealthy || comp.Status == health.StatusUnknown {
				h.logger.Error("component unhealthy",
					"component", comp.Name,
					"status", comp.Status.String(),
					"message", comp.Message,
					"response_time_ms", comp.ResponseTime.Milliseconds(),
					"error", comp.Error)
			}
		}
	}
}

// DatabaseHealthChecker checks database connectivity.
type DatabaseHealthChecker struct {
	repo    *persistence.Repository
	timeout time.Duration
}

// NewDatabaseHealthChecker creates a new database health checker.
func NewDatabaseHealthChecker(repo *persistence.Repository, timeout time.Duration) *DatabaseHealthChecker {
	return &DatabaseHealthChecker{
		repo:    repo,
		timeout: timeout,
	}
}

// Name returns the component name.
func (d *DatabaseHealthChecker) Name() string {
	return "database"
}

// Check performs a database health check by executing a simple query.
func (d *DatabaseHealthChecker) Check(ctx context.Context) health.ComponentResult {
	start := time.Now()

	// Create timeout context
	checkCtx, cancel := context.WithTimeout(ctx, d.timeout)
	defer cancel()

	// Use Ping() which executes SELECT 1 - no record-not-found logging
	err := d.repo.Ping(checkCtx)

	responseTime := time.Since(start)

	status := health.StatusHealthy
	message := "database connection successful"

	if err != nil {
		status = health.StatusUnhealthy
		message = fmt.Sprintf("database check failed: %v", err)
	}

	// Check context cancellation (timeout)
	if checkCtx.Err() != nil {
		status = health.StatusUnhealthy
		message = fmt.Sprintf("database check timeout after %s", d.timeout)
		err = checkCtx.Err()
	}

	return health.ComponentResult{
		Name:         d.Name(),
		Status:       status,
		Message:      message,
		ResponseTime: responseTime,
		CheckedAt:    start,
		Error:        err,
	}
}
