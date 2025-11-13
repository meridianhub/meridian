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

	positionkeepingv1 "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/internal/position-keeping/app"
	"github.com/meridianhub/meridian/internal/position-keeping/service"
	"github.com/meridianhub/meridian/pkg/platform/idempotency"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
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

	// Override observability config with build info
	config.Observability.ServiceVersion = Version

	logger.Info("configuration loaded",
		"environment", config.Observability.Environment,
		"grpc_port", config.Server.Port)

	// Initialize dependency container
	container, err := app.NewContainer(ctx, config, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize container: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := container.Close(closeCtx); err != nil {
			logger.Error("failed to close container", "error", err)
		}
	}()

	// Initialize idempotency service (Redis-backed or in-memory fallback)
	idempotencySvc, err := initializeIdempotencyService(config, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize idempotency service: %w", err)
	}

	// Create gRPC service
	positionKeepingService := service.NewPositionKeepingService(
		container.PositionLogRepository,
		container.EventPublisher,
		idempotencySvc,
	)

	logger.Info("position keeping service initialized")

	// Create gRPC server with interceptor chain
	var serverOptions []grpc.ServerOption

	// Add unary interceptors: tracing → auth (future) → recovery
	if container.Tracer != nil {
		serverOptions = append(serverOptions,
			grpc.ChainUnaryInterceptor(
				container.Tracer.UnaryServerInterceptor(),
				// TODO: Add auth.UnaryInterceptor() when authentication is ready
				// TODO: Add custom recovery interceptor for panic handling
			),
			grpc.ChainStreamInterceptor(
				container.Tracer.StreamServerInterceptor(),
				// TODO: Add auth.StreamInterceptor() when authentication is ready
			),
		)
	}

	grpcServer := grpc.NewServer(serverOptions...)

	// Register Position Keeping service
	positionkeepingv1.RegisterPositionKeepingServiceServer(grpcServer, positionKeepingService)

	// Register health check service
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("position-keeping", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered",
		"services", []string{"PositionKeepingService", "Health", "Reflection"})

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

	// Mark service as not serving
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	healthServer.SetServingStatus("position-keeping", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

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

// initializeIdempotencyService creates Redis-backed idempotency service or no-op fallback
func initializeIdempotencyService(config *app.Config, logger *slog.Logger) (idempotency.Service, error) {
	if !config.Redis.Enabled {
		logger.Warn("redis disabled, using no-op idempotency service",
			"warning", "idempotency guarantees are disabled - NOT SUITABLE FOR PRODUCTION",
			"note", "duplicate requests may be processed multiple times")
		return &noOpIdempotencyService{}, nil
	}

	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:            config.Redis.Address,
		Password:        config.Redis.Password,
		DB:              config.Redis.DB,
		PoolSize:        config.Redis.PoolSize,
		ConnMaxIdleTime: config.Redis.ConnMaxIdleTime,
	})

	// Verify Redis connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := redisClient.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	// Create Redis-backed idempotency service
	redisService := idempotency.NewRedisService(redisClient)

	logger.Info("redis idempotency service initialized",
		"address", config.Redis.Address,
		"db", config.Redis.DB,
		"pool_size", config.Redis.PoolSize)

	return redisService, nil
}

// noOpIdempotencyService is a fallback that provides no idempotency guarantees
// Used when Redis is disabled. NOT suitable for production use.
type noOpIdempotencyService struct{}

func (n *noOpIdempotencyService) Check(context.Context, idempotency.Key) (*idempotency.Result, error) {
	return nil, idempotency.ErrResultNotFound
}

func (n *noOpIdempotencyService) MarkPending(context.Context, idempotency.Key, time.Duration) error {
	return nil
}

func (n *noOpIdempotencyService) StoreResult(context.Context, idempotency.Result) error {
	return nil
}

func (n *noOpIdempotencyService) Delete(context.Context, idempotency.Key) error {
	return nil
}

func (n *noOpIdempotencyService) Acquire(context.Context, idempotency.Key, idempotency.LockOptions) error {
	return nil
}

func (n *noOpIdempotencyService) Release(context.Context, idempotency.Key, string) error {
	return nil
}

func (n *noOpIdempotencyService) Refresh(context.Context, idempotency.Key, string, time.Duration) error {
	return nil
}

func (n *noOpIdempotencyService) IsHeld(context.Context, idempotency.Key) (bool, error) {
	return false, nil
}
