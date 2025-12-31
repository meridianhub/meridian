package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// HealthChecker implements the gRPC health check service.
type HealthChecker struct {
	grpc_health_v1.UnimplementedHealthServer
	repo        *persistence.Repository
	logger      *slog.Logger
	serviceName string
	timeout     time.Duration
}

// HealthCheckerConfig holds configuration for the health checker.
type HealthCheckerConfig struct {
	Repository  *persistence.Repository
	Logger      *slog.Logger
	ServiceName string
	Timeout     time.Duration
}

// NewHealthChecker creates a new health checker.
func NewHealthChecker(config HealthCheckerConfig) *HealthChecker {
	if config.Timeout <= 0 {
		config.Timeout = defaults.DefaultHealthCheckTimeout
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}

	return &HealthChecker{
		repo:        config.Repository,
		logger:      config.Logger,
		serviceName: config.ServiceName,
		timeout:     config.Timeout,
	}
}

// Check implements the gRPC health check.
func (h *HealthChecker) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	// Create timeout context for health check
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()

	// Check database connectivity
	if err := h.repo.Ping(ctx); err != nil {
		h.logger.Warn("health check failed: database ping failed",
			"service", h.serviceName,
			"error", err)
		return &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
		}, nil
	}

	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}, nil
}

// Watch implements the gRPC health watch stream.
func (h *HealthChecker) Watch(req *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	// Initial status
	resp, _ := h.Check(stream.Context(), req)
	if err := stream.Send(resp); err != nil {
		return err
	}

	// Periodic status updates
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
			resp, _ := h.Check(stream.Context(), req)
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}
