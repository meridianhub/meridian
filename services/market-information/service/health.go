// Package service provides gRPC service implementations for the Market Information service.
// BIAN Service Domain: Market Information Management
package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/meridianhub/meridian/services/market-information/config"
	"github.com/meridianhub/meridian/services/market-information/observability"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/platform/db"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// HealthCheckerConfig holds configuration for the health checker.
type HealthCheckerConfig struct {
	// Database connection pool (optional during scaffolding)
	Database *db.PostgresPool
	// Logger for health check operations
	Logger *slog.Logger
	// ServiceName used in health check responses
	ServiceName string
	// CheckTimeout is the maximum duration for health checks
	CheckTimeout time.Duration
	// ServiceConfig holds service-specific configuration for external dependencies
	ServiceConfig config.Config
}

// HealthChecker implements the gRPC health check protocol with dependency aggregation.
type HealthChecker struct {
	grpc_health_v1.UnimplementedHealthServer
	logger      *slog.Logger
	aggregator  *health.Aggregator
	serviceName string
	timeout     time.Duration
}

// DefaultECBHealthCheckEndpoint is the base ECB SDMX Web Service URL used for health checks.
const DefaultECBHealthCheckEndpoint = "https://sdw-wsrest.ecb.europa.eu"

// NewHealthChecker creates a new HealthChecker with health check aggregation.
func NewHealthChecker(cfg HealthCheckerConfig) *HealthChecker {
	// Build list of health checkers
	var checkers []health.Checker

	// Add database checker if database is configured
	if cfg.Database != nil {
		checkers = append(checkers, health.NewDatabaseChecker(cfg.Database))
	}

	// Add ECB API health checker if ECB integration is enabled
	if cfg.ServiceConfig.ECB.Enabled {
		endpoint := cfg.ServiceConfig.ECB.Endpoint
		if endpoint == "" {
			endpoint = DefaultECBHealthCheckEndpoint
		}

		timeout := cfg.ServiceConfig.ECB.Timeout
		if timeout == 0 {
			timeout = 5 * time.Second // Default health check timeout
		}

		checkers = append(checkers, health.NewHTTPChecker(health.HTTPCheckerConfig{
			Name:       "ecb-api",
			Endpoint:   endpoint,
			Timeout:    timeout,
			HTTPClient: &http.Client{Timeout: timeout},
		}))
	}

	aggregator := health.NewAggregator(checkers)

	return &HealthChecker{
		logger:      cfg.Logger,
		aggregator:  aggregator,
		serviceName: cfg.ServiceName,
		timeout:     cfg.CheckTimeout,
	}
}

// Check implements the gRPC health check protocol.
func (h *HealthChecker) Check(ctx context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	// Apply timeout to health check context
	checkCtx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	// Empty service name or matching service name: check all components
	if req.Service == "" || req.Service == h.serviceName {
		report := h.aggregator.CheckAll(checkCtx)
		overallStatus := report.OverallStatus()
		grpcStatus := h.mapStatusToGRPC(overallStatus)

		// Record metrics
		h.recordHealthCheckMetrics(report)

		h.logger.Debug("health check completed",
			"overall_status", overallStatus.String(),
			"grpc_status", grpcStatus.String(),
			"component_count", len(report.Components))

		return &grpc_health_v1.HealthCheckResponse{
			Status: grpcStatus,
		}, nil
	}

	// Check if request is for a specific component
	componentResult, found := h.aggregator.CheckByName(checkCtx, req.Service)
	if found {
		grpcStatus := h.mapStatusToGRPC(componentResult.Status)

		h.logger.Debug("component health check completed",
			"component", componentResult.Name,
			"status", componentResult.Status.String(),
			"grpc_status", grpcStatus.String())

		return &grpc_health_v1.HealthCheckResponse{
			Status: grpcStatus,
		}, nil
	}

	// Service name doesn't match and component not found
	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_UNKNOWN,
	}, nil
}

// Watch implements the gRPC health watch protocol.
func (h *HealthChecker) Watch(req *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	ctx := stream.Context()

	// Send immediate health check
	resp, err := h.Check(ctx, req)
	if err != nil {
		return err
	}

	if err := stream.Send(resp); err != nil {
		h.logger.Error("failed to send initial health check", "error", err)
		return fmt.Errorf("failed to send initial health status: %w", err)
	}

	// Stream periodic updates
	ticker := time.NewTicker(h.timeout)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.logger.Debug("health watch stream closed", "reason", ctx.Err())
			return fmt.Errorf("health watch context cancelled: %w", ctx.Err())

		case <-ticker.C:
			resp, err := h.Check(ctx, req)
			if err != nil {
				h.logger.Error("health check failed during watch", "error", err)
				return err
			}

			if err := stream.Send(resp); err != nil {
				h.logger.Error("failed to send health check update", "error", err)
				return fmt.Errorf("failed to send health status update: %w", err)
			}
		}
	}
}

// mapStatusToGRPC converts internal health status to gRPC health check status.
func (h *HealthChecker) mapStatusToGRPC(status health.Status) grpc_health_v1.HealthCheckResponse_ServingStatus {
	switch status {
	case health.StatusHealthy, health.StatusDegraded:
		return grpc_health_v1.HealthCheckResponse_SERVING
	case health.StatusUnhealthy:
		return grpc_health_v1.HealthCheckResponse_NOT_SERVING
	case health.StatusUnknown:
		return grpc_health_v1.HealthCheckResponse_UNKNOWN
	default:
		return grpc_health_v1.HealthCheckResponse_UNKNOWN
	}
}

// recordHealthCheckMetrics records health check results to Prometheus.
func (h *HealthChecker) recordHealthCheckMetrics(report *health.Report) {
	for _, comp := range report.Components {
		status := "healthy"
		if comp.Status != health.StatusHealthy {
			status = comp.Status.String()
		}
		observability.RecordHealthCheck(comp.Name, status)
	}
}
