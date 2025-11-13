// Package main is the entry point for the Position Keeping service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/internal/position-keeping/app"
	"github.com/meridianhub/meridian/internal/position-keeping/interceptors"
	"github.com/meridianhub/meridian/internal/position-keeping/service"
	"github.com/meridianhub/meridian/pkg/platform/health"
	"github.com/meridianhub/meridian/pkg/platform/idempotency"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
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

// Prometheus metrics
var (
	grpcRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "position_keeping_grpc_requests_total",
			Help: "Total number of gRPC requests",
		},
		[]string{"method", "status"},
	)
	grpcRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "position_keeping_grpc_request_duration_seconds",
			Help:    "Duration of gRPC requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)
	healthCheckTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "position_keeping_health_check_total",
			Help: "Total number of health checks performed",
		},
		[]string{"component", "status"},
	)
)

func init() {
	// Register Prometheus metrics
	prometheus.MustRegister(grpcRequestsTotal)
	prometheus.MustRegister(grpcRequestDuration)
	prometheus.MustRegister(healthCheckTotal)
}

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
		"grpc_port", config.Server.Port,
		"metrics_port", config.Observability.MetricsPort)

	// Initialize dependency container
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

	// Initialize idempotency service (Redis-backed or no-op fallback)
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

	// Build interceptor chain: metrics → tracing → auth (future) → recovery
	// Order matters: metrics first for all requests, tracing for observability, recovery last to catch panics
	var unaryInterceptors []grpc.UnaryServerInterceptor
	var streamInterceptors []grpc.StreamServerInterceptor

	// 1. Metrics (always enabled)
	unaryInterceptors = append(unaryInterceptors,
		app.MetricsInterceptor(grpcRequestsTotal, grpcRequestDuration))

	// 2. Tracing (optional if OTLP endpoint configured)
	if container.Tracer != nil {
		unaryInterceptors = append(unaryInterceptors, container.Tracer.UnaryServerInterceptor())
		streamInterceptors = append(streamInterceptors, container.Tracer.StreamServerInterceptor())
	}

	// 3. Auth (JWT validation with JWKS)
	if container.AuthInterceptor != nil {
		unaryInterceptors = append(unaryInterceptors, container.AuthInterceptor.UnaryInterceptor())
		streamInterceptors = append(streamInterceptors, container.AuthInterceptor.StreamInterceptor())
		logger.Info("auth interceptor enabled in chain")
	}

	// 4. Recovery (last in chain to catch all panics)
	unaryInterceptors = append(unaryInterceptors, interceptors.RecoveryUnaryInterceptor(logger))
	streamInterceptors = append(streamInterceptors, interceptors.RecoveryStreamInterceptor(logger))

	serverOptions = append(serverOptions,
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
	)
	if len(streamInterceptors) > 0 {
		serverOptions = append(serverOptions,
			grpc.ChainStreamInterceptor(streamInterceptors...),
		)
	}

	grpcServer := grpc.NewServer(serverOptions...)

	// Register Position Keeping service
	pb.RegisterPositionKeepingServiceServer(grpcServer, positionKeepingService)

	// Register health check service
	healthServer := newHealthServer(container, logger)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered",
		"services", []string{"PositionKeepingService", "Health", "Reflection"})

	// Start HTTP server for health checks and metrics
	httpMux := http.NewServeMux()

	// Create health check aggregator
	healthCheckers := []health.Checker{
		app.NewPgxPoolChecker(container.DBPool),
	}
	healthAggregator := health.NewAggregator(healthCheckers)
	healthHandler := health.NewHTTPHandler(healthAggregator)
	healthHandler.RegisterHandlers(httpMux)

	// Add Prometheus metrics endpoint if enabled
	if config.Observability.MetricsEnabled {
		httpMux.Handle("/metrics", promhttp.Handler())
		logger.Info("metrics endpoint enabled", "path", "/metrics")
	}

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%s", config.Observability.MetricsPort),
		Handler:           httpMux,
		ReadTimeout:       10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Start HTTP server in background
	httpErrors := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server for health and metrics",
			"address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErrors <- err
		}
	}()

	// Create gRPC listener
	grpcAddress := fmt.Sprintf(":%s", config.Server.Port)
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", grpcAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", grpcAddress, err)
	}

	// Start gRPC server in background
	grpcErrors := make(chan error, 1)
	go func() {
		logger.Info("starting gRPC server", "address", grpcAddress)
		if err := grpcServer.Serve(listener); err != nil {
			grpcErrors <- err
		}
	}()

	// Wait for interrupt signal or server error
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-grpcErrors:
		return fmt.Errorf("gRPC server error: %w", err)
	case err := <-httpErrors:
		return fmt.Errorf("HTTP server error: %w", err)
	}

	// Graceful shutdown
	logger.Info("shutting down servers...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.Server.GracefulShutdownTimeout)
	defer cancel()

	// Shutdown HTTP server
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	} else {
		logger.Info("HTTP server stopped gracefully")
	}

	// Gracefully stop gRPC server
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	// Wait for graceful stop or timeout
	select {
	case <-stopped:
		logger.Info("gRPC server stopped gracefully")
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
	// Use existing PgxPoolChecker for database health check
	checker := app.NewPgxPoolChecker(h.container.DBPool)
	result := checker.Check(ctx)

	grpcStatus := grpc_health_v1.HealthCheckResponse_SERVING
	status := "healthy"
	if result.Status == health.StatusUnhealthy {
		grpcStatus = grpc_health_v1.HealthCheckResponse_NOT_SERVING
		status = "unhealthy"
		h.logger.Warn("health check failed: database ping failed", "error", result.Error, "response_time", result.ResponseTime)
	}

	// Record health check metric
	healthCheckTotal.WithLabelValues(result.Name, status).Inc()

	return &grpc_health_v1.HealthCheckResponse{
		Status: grpcStatus,
	}, nil
}

// Watch performs a streaming health check (required by interface)
func (h *healthServer) Watch(_ *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	ctx := stream.Context()

	// Send initial status with timeout
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	resp, _ := h.Check(checkCtx, nil)
	cancel()
	if err := stream.Send(resp); err != nil {
		return err
	}

	// Keep the stream open and periodically check health
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			resp, _ := h.Check(checkCtx, nil)
			cancel()
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}
