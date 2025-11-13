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
	"github.com/meridianhub/meridian/internal/position-keeping/service"
	"github.com/meridianhub/meridian/pkg/platform/health"
	"github.com/meridianhub/meridian/pkg/platform/idempotency"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

	// Build interceptor chain
	var unaryInterceptors []grpc.UnaryServerInterceptor
	var streamInterceptors []grpc.StreamServerInterceptor

	// Add metrics interceptor (always enabled)
	unaryInterceptors = append(unaryInterceptors,
		app.MetricsInterceptor(grpcRequestsTotal, grpcRequestDuration))

	// Add tracing interceptors if configured
	if container.Tracer != nil {
		unaryInterceptors = append(unaryInterceptors, container.Tracer.UnaryServerInterceptor())
		streamInterceptors = append(streamInterceptors, container.Tracer.StreamServerInterceptor())
	}

	serverOptions = append(serverOptions,
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
	)
	if len(streamInterceptors) > 0 {
		serverOptions = append(serverOptions,
			grpc.ChainStreamInterceptor(streamInterceptors...),
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
	start := time.Now()
	err := h.container.DBPool.Ping(ctx)
	responseTime := time.Since(start)

	status := "healthy"
	grpcStatus := grpc_health_v1.HealthCheckResponse_SERVING

	if err != nil {
		h.logger.Warn("health check failed: database ping failed", "error", err, "response_time", responseTime)
		status = "unhealthy"
		grpcStatus = grpc_health_v1.HealthCheckResponse_NOT_SERVING
	}

	// Record health check metric
	healthCheckTotal.WithLabelValues("database", status).Inc()

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
