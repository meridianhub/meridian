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
	"strings"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	"github.com/meridianhub/meridian/services/position-keeping/app"
	"github.com/meridianhub/meridian/services/position-keeping/observability"
	"github.com/meridianhub/meridian/services/position-keeping/service"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/pkg/interceptors"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	pkobservability "github.com/meridianhub/meridian/shared/platform/observability"
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

	// Run the service with retry for transient startup errors
	if err := bootstrap.RunWithRetry(
		func() error { return run(logger) },
		bootstrap.WithRetryLogger(logger),
	); err != nil {
		logger.Error("service failed to start", "error", err)
		os.Exit(1)
	}

	logger.Info("service stopped gracefully")
}

func run(logger *slog.Logger) error {
	// Use a run-scoped context so lazy resolution goroutines stop when run() returns
	// (e.g., during RunWithRetry retries or shutdown).
	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	// Load configuration (permanent error if invalid)
	config, err := app.LoadConfig()
	if err != nil {
		return bootstrap.Permanent(fmt.Errorf("failed to load configuration: %w", err))
	}
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

	// Initialize and start event outbox worker (if Kafka enabled).
	outboxShutdownCh := make(chan func(), 1)
	kafkaCleanupCh := make(chan func(), 1)
	startOutboxWorker(ctx, container, config, logger, outboxShutdownCh, kafkaCleanupCh)

	// Start compaction worker in background (if enabled)
	compactionWorkerCancel := startCompactionWorkerAsync(container, logger)
	if compactionWorkerCancel != nil {
		defer compactionWorkerCancel()
	}

	// Create gRPC server, register services, and start listening
	grpcServer, listener, err := setupGRPCServer(ctx, config, container, logger)
	if err != nil {
		return err
	}

	// Start HTTP and gRPC servers
	httpErrors := make(chan error, 1)
	httpServer := startHTTPServer(config, container, logger, httpErrors)
	grpcErrors := serveGRPCAsync(grpcServer, listener, logger)

	return awaitAndShutdown(ctx, config, grpcServer, httpServer, container,
		outboxShutdownCh, kafkaCleanupCh, compactionWorkerCancel,
		grpcErrors, httpErrors, runCancel, logger)
}

// startCompactionWorkerAsync starts the compaction worker in a background goroutine if enabled.
// Returns a cancel function to stop the worker, or nil if no worker was started.
func startCompactionWorkerAsync(container *app.Container, logger *slog.Logger) context.CancelFunc {
	if container.CompactionWorker == nil {
		return nil
	}
	compactionCtx, cancel := context.WithCancel(context.Background())
	go func() {
		if err := container.CompactionWorker.Start(compactionCtx); err != nil {
			logger.Error("compaction worker error", "error", err)
		}
	}()
	return cancel
}

// serveGRPCAsync starts the gRPC server in a background goroutine and returns an error channel.
func serveGRPCAsync(grpcServer *grpc.Server, listener net.Listener, logger *slog.Logger) chan error {
	grpcErrors := make(chan error, 1)
	go func() {
		logger.Info("starting gRPC server", "address", listener.Addr().String())
		if err := grpcServer.Serve(listener); err != nil {
			grpcErrors <- err
		}
	}()
	return grpcErrors
}

// setupGRPCServer creates the gRPC server, registers services, and creates the TCP listener.
func setupGRPCServer(ctx context.Context, config *app.Config, container *app.Container, logger *slog.Logger) (*grpc.Server, net.Listener, error) {
	positionKeepingService, err := service.NewPositionKeepingService(
		container.PositionLogRepository,
		container.MeasurementRepository,
		container.EventPublisher,
		container.IdempotencyService,
		container.OutboxPublisher,
		container.ServiceOpts...,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create position keeping service: %w", err)
	}
	logger.Info("position keeping service initialized")

	tracer := container.Tracer
	if tracer == nil {
		var tracerErr error
		tracer, tracerErr = pkobservability.NewTracer(ctx, pkobservability.TracerConfig{
			ServiceName:  "position-keeping",
			OTLPEndpoint: "localhost:4317",
			Enabled:      false,
		})
		if tracerErr != nil {
			return nil, nil, fmt.Errorf("failed to create no-op tracer: %w", tracerErr)
		}
	}

	grpcServer, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(container.AuthInterceptor).
		WithUnaryInterceptor(interceptors.MetricsInterceptor(grpcRequestsTotal, grpcRequestDuration)).
		Build()
	if err != nil {
		return nil, nil, bootstrap.Permanent(fmt.Errorf("failed to build grpc server: %w", err))
	}

	healthCheckers := []health.Checker{
		observability.NewPgxPoolChecker(container.DBPool),
	}
	if container.RedisClient != nil {
		healthCheckers = append(healthCheckers, observability.NewRedisChecker(container.RedisClient))
	}
	healthAggregator := health.NewAggregator(healthCheckers)

	pb.RegisterPositionKeepingServiceServer(grpcServer, positionKeepingService)
	healthServer := newHealthServer(healthAggregator, logger)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered",
		"services", []string{"PositionKeepingService", "Health", "Reflection"})

	grpcAddress := fmt.Sprintf(":%s", config.Server.Port)
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", grpcAddress)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to listen on %s: %w", grpcAddress, err)
	}

	return grpcServer, listener, nil
}

// startHTTPServer creates and starts the HTTP server for health checks and metrics.
func startHTTPServer(config *app.Config, container *app.Container, logger *slog.Logger, httpErrors chan error) *http.Server {
	healthCheckers := []health.Checker{
		observability.NewPgxPoolChecker(container.DBPool),
	}
	if container.RedisClient != nil {
		healthCheckers = append(healthCheckers, observability.NewRedisChecker(container.RedisClient))
	}
	healthAggregator := health.NewAggregator(healthCheckers)

	httpMux := http.NewServeMux()
	healthHandler := health.NewHTTPHandler(healthAggregator)
	healthHandler.RegisterHandlers(httpMux)
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

	go func() {
		logger.Info("starting HTTP server for health and metrics",
			"address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErrors <- err
		}
	}()

	return httpServer
}

// awaitAndShutdown waits for a shutdown signal or server error, then orchestrates graceful shutdown.
func awaitAndShutdown(
	_ context.Context,
	config *app.Config,
	grpcServer *grpc.Server,
	httpServer *http.Server,
	container *app.Container,
	outboxShutdownCh, kafkaCleanupCh chan func(),
	compactionWorkerCancel context.CancelFunc,
	grpcErrors, httpErrors chan error,
	runCancel context.CancelFunc,
	logger *slog.Logger,
) error {
	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	var runErr error
	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-grpcErrors:
		logger.Error("gRPC server error", "error", err)
		runErr = fmt.Errorf("gRPC server error: %w", err)
	case err := <-httpErrors:
		logger.Error("HTTP server error", "error", err)
		runErr = fmt.Errorf("HTTP server error: %w", err)
	}

	logger.Info("shutting down servers...")
	runCancel()

	// Shutdown outbox worker
	select {
	case shutdownFn := <-outboxShutdownCh:
		logger.Info("stopping event outbox worker...")
		shutdownFn()
		logger.Info("event outbox worker stopped")
	default:
	}

	// Close the lazily-resolved Kafka producer
	select {
	case cleanupFn := <-kafkaCleanupCh:
		cleanupFn()
	default:
	}

	// Shutdown compaction worker
	if container.CompactionWorker != nil {
		logger.Info("stopping compaction worker...")
		if compactionWorkerCancel != nil {
			compactionWorkerCancel()
		}
		container.CompactionWorker.Stop()
		logger.Info("compaction worker stopped")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.Server.GracefulShutdownTimeout)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	} else {
		logger.Info("HTTP server stopped gracefully")
	}

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		logger.Info("gRPC server stopped gracefully")
	case <-shutdownCtx.Done():
		logger.Warn("graceful shutdown timeout, forcing stop")
		grpcServer.Stop()
	}

	return runErr
}

// startOutboxWorker initializes and starts the event outbox worker with lazy Kafka resolution.
func startOutboxWorker(ctx context.Context, container *app.Container, config *app.Config, logger *slog.Logger, outboxShutdownCh, kafkaCleanupCh chan func()) {
	if container.KafkaProducer() != nil {
		startOutboxWorkerDirect(container, logger, outboxShutdownCh) //nolint:contextcheck // worker uses its own context for lifecycle management
	} else if config.Kafka.Enabled {
		startOutboxWorkerLazy(ctx, container, config, logger, outboxShutdownCh, kafkaCleanupCh)
	} else {
		logger.Info("event outbox worker disabled (kafka not configured)")
	}
}

// startOutboxWorkerDirect starts the outbox worker with an already-available Kafka producer.
func startOutboxWorkerDirect(container *app.Container, logger *slog.Logger, outboxShutdownCh chan func()) {
	workerConfig := events.DefaultWorkerConfig("position-keeping")
	w := events.NewWorker(
		container.OutboxRepository,
		container.KafkaProducer(),
		workerConfig,
		logger,
	)

	workerCtx, cancel := context.WithCancel(context.Background())
	w.Start(workerCtx)
	outboxShutdownCh <- func() {
		cancel()
		w.Stop()
	}
}

// startOutboxWorkerLazy starts the outbox worker with lazy Kafka producer resolution.
func startOutboxWorkerLazy(ctx context.Context, container *app.Container, config *app.Config, logger *slog.Logger, outboxShutdownCh, kafkaCleanupCh chan func()) {
	lazyKafka := bootstrap.NewLazyClient(ctx, "kafka-producer",
		func(_ context.Context) (*kafka.ProtoProducer, func(), error) {
			producerConfig := kafka.ProducerConfig{
				BootstrapServers: strings.Join(config.Kafka.Brokers, ","),
				ClientID:         "position-keeping-service",
				Acks:             "all",
				Retries:          3,
				Compression:      "snappy",
			}
			producer, err := kafka.NewProtoProducer(producerConfig)
			if err != nil {
				return nil, nil, err
			}
			return producer, func() { //nolint:contextcheck // FlushWithTimeout manages its own timeout
				if remaining := producer.FlushWithTimeout(5000); remaining > 0 {
					logger.Warn("some Kafka messages not delivered before close", "remaining", remaining)
				}
				producer.Close()
			}, nil
		},
		bootstrap.WithLazyLogger(logger),
		bootstrap.WithLazyOnCleanup(func(cleanup func()) {
			kafkaCleanupCh <- cleanup
		}),
	)

	go func() {
		for {
			producer, err := lazyKafka.Get()
			if err == nil {
				workerConfig := events.DefaultWorkerConfig("position-keeping")
				w := events.NewWorker(
					container.OutboxRepository,
					producer,
					workerConfig,
					logger,
				)
				workerCtx, cancel := context.WithCancel(context.Background())
				w.Start(workerCtx) //nolint:contextcheck // Worker needs independent context for shutdown control
				logger.Info("outbox worker started after lazy Kafka resolution")
				outboxShutdownCh <- func() {
					cancel()
					w.Stop()
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(1 * time.Second):
			}
		}
	}()
	logger.Info("outbox worker pending lazy Kafka producer resolution")
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
	report := h.aggregator.CheckAll(ctx)

	grpcStatus := grpc_health_v1.HealthCheckResponse_SERVING
	overallStatus := report.OverallStatus()
	if overallStatus == health.StatusUnhealthy || overallStatus == health.StatusDegraded {
		grpcStatus = grpc_health_v1.HealthCheckResponse_NOT_SERVING
		h.logger.Warn("health check failed",
			"status", overallStatus,
			"checked_at", report.CheckedAt)
	}

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

	checkCtx, cancel := context.WithTimeout(ctx, defaults.DefaultHealthCheckTimeout)
	resp, _ := h.Check(checkCtx, nil)
	cancel()
	if err := stream.Send(resp); err != nil {
		return err
	}

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
