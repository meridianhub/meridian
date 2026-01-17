// Package service provides gRPC service implementations for the Market Information service.
// BIAN Service Domain: Market Information Management
package service

import (
	"context"
	"log/slog"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// HealthCheckerConfig holds configuration for the health checker.
type HealthCheckerConfig struct {
	// Logger for health check operations
	Logger *slog.Logger
	// ServiceName used in health check responses
	ServiceName string
	// CheckTimeout is the maximum duration for health checks
	CheckTimeout time.Duration
}

// HealthChecker implements the gRPC health check protocol.
type HealthChecker struct {
	grpc_health_v1.UnimplementedHealthServer
	logger      *slog.Logger
	serviceName string
	timeout     time.Duration
}

// NewHealthChecker creates a new HealthChecker.
func NewHealthChecker(cfg HealthCheckerConfig) *HealthChecker {
	return &HealthChecker{
		logger:      cfg.Logger,
		serviceName: cfg.ServiceName,
		timeout:     cfg.CheckTimeout,
	}
}

// Check implements the gRPC health check protocol.
func (h *HealthChecker) Check(_ context.Context, req *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	// For now, always report healthy since we don't have dependencies to check
	// TODO: Add database connectivity check when repository is implemented
	h.logger.Debug("health check", "service", req.Service)

	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}, nil
}

// Watch implements the gRPC health watch protocol.
func (h *HealthChecker) Watch(_ *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	// Send initial status
	if err := stream.Send(&grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}); err != nil {
		return status.Errorf(codes.Internal, "failed to send health response: %v", err)
	}

	// Keep the stream open until client disconnects
	<-stream.Context().Done()
	return nil
}
