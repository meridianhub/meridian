// Package main is the entry point for the Position Keeping service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/internal/position-keeping/app"
	"github.com/meridianhub/meridian/internal/position-keeping/service"
	"github.com/meridianhub/meridian/pkg/platform/idempotency"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Build information set via ldflags during compilation
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	// Initialize structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting position-keeping service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	// Run the service
	if err := run(logger); err != nil {
		logger.Error("service failed", "error", err)
		os.Exit(1)
	}

	logger.Info("service stopped gracefully")
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	// Load configuration
	config, err := app.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger.Info("configuration loaded successfully")

	// Initialize dependency injection container
	container, err := app.NewContainer(ctx, config, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize container: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), config.Server.GracefulShutdownTimeout)
		defer cancel()
		if err := container.Close(shutdownCtx); err != nil {
			logger.Error("failed to close container", "error", err)
		}
	}()

	logger.Info("dependency container initialized")

	// Create idempotency service
	// For now use nil (idempotency features disabled) until Redis integration is ready
	// TODO: Wire up idempotency.NewRedisService(redisClient) when Redis is configured
	var idempotencySvc idempotency.Service

	// Create gRPC service
	positionKeepingService := service.NewPositionKeepingService(
		container.PositionLogRepository,
		container.EventPublisher,
		idempotencySvc,
	)

	logger.Info("services created")

	// Create gRPC server with observability interceptors
	var serverOptions []grpc.ServerOption
	if container.Tracer != nil {
		serverOptions = append(serverOptions,
			grpc.ChainUnaryInterceptor(
				container.Tracer.UnaryServerInterceptor(),
			),
			grpc.ChainStreamInterceptor(
				container.Tracer.StreamServerInterceptor(),
			),
		)
	}

	grpcServer := grpc.NewServer(serverOptions...)

	// Register services
	pb.RegisterPositionKeepingServiceServer(grpcServer, positionKeepingService)

	// Register health check service
	healthServer := newHealthServer(container, logger)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Create listener
	address := fmt.Sprintf(":%s", config.Server.Port)
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	// Start gRPC server in background
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Wait for interrupt signal or server error
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	}

	// Graceful shutdown
	logger.Info("shutting down server...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.Server.GracefulShutdownTimeout)
	defer cancel()

	// Gracefully stop gRPC server
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	// Wait for graceful stop or timeout
	select {
	case <-stopped:
		logger.Info("server stopped gracefully")
	case <-shutdownCtx.Done():
		logger.Warn("graceful shutdown timeout, forcing stop")
		grpcServer.Stop()
	}

	return nil
}

// healthServer implements the gRPC health checking protocol
type healthServer struct {
	grpc_health_v1.UnimplementedHealthServer
	container *app.Container
	logger    *slog.Logger
}

func newHealthServer(container *app.Container, logger *slog.Logger) *healthServer {
	return &healthServer{
		container: container,
		logger:    logger,
	}
}

// Check performs a health check
func (h *healthServer) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	// Check database connectivity
	if err := h.container.DBPool.Ping(ctx); err != nil {
		h.logger.Warn("health check failed: database ping failed", "error", err)
		return &grpc_health_v1.HealthCheckResponse{
			Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
		}, nil
	}

	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}, nil
}

// Watch performs a streaming health check (required by interface)
func (h *healthServer) Watch(_ *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	// Send initial status
	if err := stream.Send(&grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}); err != nil {
		return err
	}

	// Keep the stream open and periodically check health
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := h.container.DBPool.Ping(ctx); err != nil {
				cancel()
				if sendErr := stream.Send(&grpc_health_v1.HealthCheckResponse{
					Status: grpc_health_v1.HealthCheckResponse_NOT_SERVING,
				}); sendErr != nil {
					return sendErr
				}
			} else {
				cancel()
				if sendErr := stream.Send(&grpc_health_v1.HealthCheckResponse{
					Status: grpc_health_v1.HealthCheckResponse_SERVING,
				}); sendErr != nil {
					return sendErr
				}
			}
		}
	}
}
