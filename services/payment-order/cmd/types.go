package main

import (
	"context"
	"log/slog"
	"strings"

	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/services/payment-order/service"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// localPaymentOrderClient wraps the local service to implement the client interface.
type localPaymentOrderClient struct {
	service *service.Service
}

func (c *localPaymentOrderClient) UpdatePaymentOrder(ctx context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
	return c.service.UpdatePaymentOrder(ctx, req)
}

// simpleHealthServer implements grpc_health_v1.HealthServer with basic checks.
type simpleHealthServer struct {
	grpc_health_v1.UnimplementedHealthServer
}

func (s *simpleHealthServer) Check(_ context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}, nil
}

func (s *simpleHealthServer) Watch(_ *grpc_health_v1.HealthCheckRequest, server grpc_health_v1.Health_WatchServer) error {
	// Send initial status
	if err := server.Send(&grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}); err != nil {
		return err
	}
	// Block until context is done to keep stream open
	<-server.Context().Done()
	return server.Context().Err()
}

// parseLogLevel converts a string log level to slog.Level.
// Supports: debug, info, warn, error (case-insensitive). Defaults to info.
func parseLogLevel(levelStr string) slog.Level {
	switch strings.ToLower(levelStr) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
