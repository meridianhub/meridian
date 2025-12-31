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
	"github.com/meridianhub/meridian/services/position-keeping/app"
	"github.com/meridianhub/meridian/services/position-keeping/observability"
	"github.com/meridianhub/meridian/services/position-keeping/service"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/interceptors"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/events"
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

	// Initialize and start event outbox worker (if Kafka enabled)
	// TODO(tm:bian-alignment.14): Make worker config values (batch_size, poll_interval, max_retries)
	// configurable via environment variables for production tuning.
	var outboxWorker *events.Worker
	var workerCancel context.CancelFunc
	if container.KafkaProducer() != nil {
		workerConfig := events.DefaultWorkerConfig("position-keeping")
		outboxWorker = events.NewWorker(
			container.OutboxRepository,
			container.KafkaProducer(),
			workerConfig,
			logger,
		)

		// Start worker in background
		var workerCtx context.Context
		workerCtx, workerCancel = context.WithCancel(context.Background())
		defer workerCancel() // Safety net; primary shutdown goes through explicit cancellation
		outboxWorker.Start(workerCtx)
	} else {
		logger.Info("event outbox worker disabled (kafka not configured)")
	}

	// Create idempotency service
	var idempotencySvc idempotency.Service
	if container.RedisClient != nil {
		idempotencySvc = idempotency.NewRedisService(container.RedisClient)
		logger.Info("idempotency service enabled with Redis")
	} else {
		logger.Info("idempotency service disabled (Redis not configured)")
	}

	// Create gRPC service
	positionKeepingService, err := service.NewPositionKeepingService(
		container.PositionLogRepository,
		container.MeasurementRepository,
		container.EventPublisher,
		idempotencySvc,
	)
	if err != nil {
		return fmt.Errorf("failed to create position keeping service: %w", err)
	}

	logger.Info("position keeping service initialized")

	// Create gRPC server with interceptor chain
	var serverOptions []grpc.ServerOption

	// Build interceptor chain: metrics → tracing → auth (future) → recovery
	// Order matters: metrics first for all requests, tracing for observability, recovery last to catch panics
	var unaryInterceptors []grpc.UnaryServerInterceptor
	var streamInterceptors []grpc.StreamServerInterceptor

	// 1. Metrics (always enabled)
	unaryInterceptors = append(unaryInterceptors,
		interceptors.MetricsInterceptor(grpcRequestsTotal, grpcRequestDuration))

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
	} else {
		// When auth is disabled, use TenantExtractionInterceptor to get tenant from header
		unaryInterceptors = append(unaryInterceptors, auth.TenantExtractionInterceptor())
		streamInterceptors = append(streamInterceptors, auth.TenantExtractionStreamInterceptor())
		logger.Info("tenant extraction interceptor enabled (auth disabled)")
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

	// Create health check aggregator (used by both gRPC and HTTP)
	healthCheckers := []health.Checker{
		observability.NewPgxPoolChecker(container.DBPool),
	}
	// Add Redis health checker if Redis is enabled
	if container.RedisClient != nil {
		healthCheckers = append(healthCheckers, observability.NewRedisChecker(container.RedisClient))
	}
	healthAggregator := health.NewAggregator(healthCheckers)

	// Register Position Keeping service
	pb.RegisterPositionKeepingServiceServer(grpcServer, positionKeepingService)

	// Register health check service (uses aggregator for all components)
	healthServer := newHealthServer(healthAggregator, logger)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered",
		"services", []string{"PositionKeepingService", "Health", "Reflection"})

	// Start HTTP server for health checks and metrics
	httpMux := http.NewServeMux()

	// Register HTTP health handlers (using same aggregator as gRPC)
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

	// Shutdown outbox worker before stopping servers
	// TODO(tm:bian-alignment.14): Add a shutdown timeout mechanism to prevent indefinite blocking
	// if the worker fails to stop gracefully (e.g., Kafka broker unreachable).
	if outboxWorker != nil {
		logger.Info("stopping event outbox worker...")
		if workerCancel != nil {
			workerCancel() // Cancel context first to signal worker to stop accepting new work
		}
		outboxWorker.Stop() // Blocks until current batch completes and Kafka flush finishes
		logger.Info("event outbox worker stopped")
	}

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
	aggregator *health.Aggregator
	logger     *slog.Logger
}

func newHealthServer(aggregator *health.Aggregator, logger *slog.Logger) *healthServer {
	return &healthServer{
		aggregator: aggregator,
		logger:     logger,
	}
}

// Check performs a health check
func (h *healthServer) Check(ctx context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	// Check all components using aggregator
	report := h.aggregator.CheckAll(ctx)

	grpcStatus := grpc_health_v1.HealthCheckResponse_SERVING
	overallStatus := report.OverallStatus()
	if overallStatus == health.StatusUnhealthy || overallStatus == health.StatusDegraded {
		grpcStatus = grpc_health_v1.HealthCheckResponse_NOT_SERVING
		h.logger.Warn("health check failed",
			"status", overallStatus,
			"checked_at", report.CheckedAt)
	}

	// Record metrics for each component
	for _, component := range report.Components {
		status := "healthy"
		if component.Status == health.StatusUnhealthy {
			status = "unhealthy"
			h.logger.Warn("component health check failed",
				"component", component.Name,
				"error", component.Error,
				"response_time", component.ResponseTime)
		}
		healthCheckTotal.WithLabelValues(component.Name, status).Inc()
	}

	return &grpc_health_v1.HealthCheckResponse{
		Status: grpcStatus,
	}, nil
}

// Watch performs a streaming health check (required by interface)
func (h *healthServer) Watch(_ *grpc_health_v1.HealthCheckRequest, stream grpc_health_v1.Health_WatchServer) error {
	ctx := stream.Context()

	// Send initial status with timeout
	checkCtx, cancel := context.WithTimeout(ctx, defaults.DefaultHealthCheckTimeout)
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
			checkCtx, cancel := context.WithTimeout(ctx, defaults.DefaultHealthCheckTimeout)
			resp, _ := h.Check(checkCtx, nil)
			cancel()
			if err := stream.Send(resp); err != nil {
				return err
			}
		}
	}
}
