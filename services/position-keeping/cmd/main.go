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

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	internalaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/internal_account/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/position_keeping/v1"
	referencedatav1 "github.com/meridianhub/meridian/api/proto/meridian/reference_data/v1"
	"github.com/meridianhub/meridian/services/position-keeping/app"
	"github.com/meridianhub/meridian/services/position-keeping/observability"
	"github.com/meridianhub/meridian/services/position-keeping/service"
	"github.com/meridianhub/meridian/services/position-keeping/worker"
	"github.com/meridianhub/meridian/shared/pkg/health"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/interceptors"
	"github.com/meridianhub/meridian/shared/pkg/refdata"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	pkobservability "github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Build information set via ldflags during compilation
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Static errors for production environment requirements
var (
	ErrRedisRequiredInProduction = errors.New("redis required for idempotency in production environment")
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

	// Initialize and start event outbox worker (if Kafka enabled).
	// Channels communicate the worker shutdown and Kafka cleanup functions back to the
	// main goroutine to avoid data races when started lazily inside a goroutine.
	outboxShutdownCh := make(chan func(), 1)
	kafkaCleanupCh := make(chan func(), 1)
	if container.KafkaProducer() != nil {
		workerConfig := events.DefaultWorkerConfig("position-keeping")
		w := events.NewWorker(
			container.OutboxRepository,
			container.KafkaProducer(),
			workerConfig,
			logger,
		)

		// Start worker in background
		workerCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		w.Start(workerCtx)
		outboxShutdownCh <- func() {
			cancel()
			w.Stop()
		}
	} else if config.Kafka.Enabled {
		// Kafka is enabled but producer was not available at startup - use lazy resolution
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

		// Start outbox worker once Kafka producer resolves.
		// Sends shutdown function back via channel once the worker is running.
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
	} else {
		logger.Info("event outbox worker disabled (kafka not configured)")
	}

	// Initialize and start compaction worker (if enabled)
	var compactionWorker *worker.CompactionWorker
	var compactionWorkerCancel context.CancelFunc
	if config.Compaction.Enabled {
		compactionConfig := worker.CompactionConfig{
			RunInterval:       config.Compaction.RunInterval,
			FragmentThreshold: config.Compaction.FragmentThreshold,
			BatchSize:         config.Compaction.BatchSize,
		}
		var workerErr error
		compactionWorker, workerErr = worker.NewCompactionWorker(container.DBPool, compactionConfig, logger)
		if workerErr != nil {
			return fmt.Errorf("failed to create compaction worker: %w", workerErr)
		}

		// Start compaction worker in background
		var compactionWorkerCtx context.Context
		compactionWorkerCtx, compactionWorkerCancel = context.WithCancel(context.Background())
		defer compactionWorkerCancel() // Safety net; primary shutdown goes through explicit cancellation
		go func() {
			if err := compactionWorker.Start(compactionWorkerCtx); err != nil {
				logger.Error("compaction worker error", "error", err)
			}
		}()
		logger.Info("compaction worker enabled",
			"run_interval", config.Compaction.RunInterval,
			"fragment_threshold", config.Compaction.FragmentThreshold,
			"batch_size", config.Compaction.BatchSize)
	} else {
		logger.Info("compaction worker disabled")
	}

	// Create idempotency service.
	// In production: fail fast if Redis is enabled but unavailable (idempotency is critical).
	// In non-production: use NoopService for graceful degradation with metrics.
	// If Redis is not configured: use NoopService unconditionally.
	var idempotencySvc idempotency.Service
	if container.RedisClient != nil {
		idempotencySvc = idempotency.NewRedisService(container.RedisClient)
		observability.SetNoopIdempotencyActive(false)
		logger.Info("idempotency service enabled with Redis")
	} else if config.Redis.Enabled {
		// Redis is configured but was not available at container startup
		if env.IsProduction() {
			logger.Error("CRITICAL: Redis unavailable in production - failing fast",
				"environment", os.Getenv("ENVIRONMENT"))
			return bootstrap.Permanent(ErrRedisRequiredInProduction)
		}
		logger.Warn("Redis not available at startup, using noop idempotency service - DEVELOPMENT ONLY",
			"environment", os.Getenv("ENVIRONMENT"))
		idempotencySvc = idempotency.NewNoopService(logger)
		observability.SetNoopIdempotencyActive(true)
		observability.RecordServiceDegradation(observability.ComponentIdempotency, observability.DegradationReasonStartupFallback)
	} else {
		logger.Warn("Redis not configured, using noop idempotency service - DEVELOPMENT ONLY",
			"environment", os.Getenv("ENVIRONMENT"))
		idempotencySvc = idempotency.NewNoopService(logger)
		observability.SetNoopIdempotencyActive(true)
		observability.RecordServiceDegradation(observability.ComponentIdempotency, observability.DegradationReasonStartupFallback)
	}

	// Create account validator if validation is enabled
	var serviceOpts []service.Option
	var currentAccountConn *grpc.ClientConn
	var internalAccountConn *grpc.ClientConn

	if config.AccountValidation.Enabled {
		var currentAccountValidator *service.CurrentAccountValidator
		var internalAccountValidator *service.InternalAccountValidator

		// Create Current Account validator if URL is configured
		if config.AccountValidation.CurrentAccountServiceURL != "" {
			var connErr error
			currentAccountConn, connErr = grpc.NewClient(
				config.AccountValidation.CurrentAccountServiceURL,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			if connErr != nil {
				return fmt.Errorf("failed to create current account client at %s: %w",
					config.AccountValidation.CurrentAccountServiceURL, connErr)
			}

			currentAccountClient := currentaccountv1.NewCurrentAccountServiceClient(currentAccountConn)
			var validatorErr error
			currentAccountValidator, validatorErr = service.NewCurrentAccountValidator(service.CurrentAccountValidatorConfig{
				Client:        &currentAccountClientAdapter{client: currentAccountClient},
				Logger:        logger,
				CacheTTL:      config.AccountValidation.CacheTTL,
				LookupTimeout: config.AccountValidation.ConnectionTimeout,
			})
			if validatorErr != nil {
				currentAccountConn.Close()
				return fmt.Errorf("failed to create current account validator: %w", validatorErr)
			}
			logger.Info("current account validator configured",
				"url", config.AccountValidation.CurrentAccountServiceURL)
		}

		// Create Internal Account validator if URL is configured
		if config.AccountValidation.InternalAccountServiceURL != "" {
			var connErr error
			internalAccountConn, connErr = grpc.NewClient(
				config.AccountValidation.InternalAccountServiceURL,
				grpc.WithTransportCredentials(insecure.NewCredentials()),
			)
			if connErr != nil {
				if currentAccountConn != nil {
					currentAccountConn.Close()
				}
				return fmt.Errorf("failed to create internal account client at %s: %w",
					config.AccountValidation.InternalAccountServiceURL, connErr)
			}

			internalAccountClient := internalaccountv1.NewInternalAccountServiceClient(internalAccountConn)
			var validatorErr error
			internalAccountValidator, validatorErr = service.NewInternalAccountValidator(service.InternalAccountValidatorConfig{
				Client:        &internalAccountClientAdapter{client: internalAccountClient},
				Logger:        logger,
				CacheTTL:      config.AccountValidation.CacheTTL,
				LookupTimeout: config.AccountValidation.ConnectionTimeout,
			})
			if validatorErr != nil {
				if currentAccountConn != nil {
					currentAccountConn.Close()
				}
				internalAccountConn.Close()
				return fmt.Errorf("failed to create internal account validator: %w", validatorErr)
			}
			logger.Info("internal account validator configured",
				"url", config.AccountValidation.InternalAccountServiceURL)
		}

		// Create composite validator that checks both services
		compositeValidator, compositeErr := service.NewCompositeAccountValidator(service.CompositeAccountValidatorConfig{
			CurrentAccountValidator:  currentAccountValidator,
			InternalAccountValidator: internalAccountValidator,
			Logger:                   logger,
		})
		if compositeErr != nil {
			if currentAccountConn != nil {
				currentAccountConn.Close()
			}
			if internalAccountConn != nil {
				internalAccountConn.Close()
			}
			return fmt.Errorf("failed to create composite account validator: %w", compositeErr)
		}

		serviceOpts = append(serviceOpts,
			service.WithAccountValidator(compositeValidator),
			service.WithAccountValidationEnabled(true),
		)

		logger.Info("account validation enabled",
			"current_account_url", config.AccountValidation.CurrentAccountServiceURL,
			"internal_account_url", config.AccountValidation.InternalAccountServiceURL,
			"cache_ttl", config.AccountValidation.CacheTTL)
	} else {
		logger.Info("account validation disabled")
	}

	// Ensure account service connections are closed on shutdown
	defer func() {
		if currentAccountConn != nil {
			if err := currentAccountConn.Close(); err != nil {
				logger.Error("failed to close current account connection", "error", err)
			}
		}
		if internalAccountConn != nil {
			if err := internalAccountConn.Close(); err != nil {
				logger.Error("failed to close internal account connection", "error", err)
			}
		}
	}()

	// Initialize InstrumentResolver from Reference Data service (optional)
	var refDataConn *grpc.ClientConn
	if config.ReferenceData.ServiceURL != "" {
		var connErr error
		refDataConn, connErr = grpc.NewClient(
			config.ReferenceData.ServiceURL,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if connErr != nil {
			return fmt.Errorf("failed to create reference data client at %s: %w",
				config.ReferenceData.ServiceURL, connErr)
		}

		refDataClient := referencedatav1.NewReferenceDataServiceClient(refDataConn)
		dataSource := refdata.NewGRPCDataSource(refDataClient)
		cachedResolver := refdata.NewCachedResolver(dataSource, refdata.CachedResolverConfig{
			Logger: logger,
		})

		// Preload cache at startup (log warning on failure, don't block startup)
		if preloadErr := cachedResolver.Preload(ctx); preloadErr != nil {
			logger.Warn("failed to preload instrument cache from reference data, will resolve on demand",
				"error", preloadErr)
		}

		serviceOpts = append(serviceOpts, service.WithInstrumentResolver(cachedResolver))
		logger.Info("instrument resolver configured",
			"url", config.ReferenceData.ServiceURL)
	} else {
		logger.Info("instrument resolver disabled (REFERENCE_DATA_SERVICE_URL not configured)")
	}

	// Ensure reference data connection is closed on shutdown
	defer func() {
		if refDataConn != nil {
			if err := refDataConn.Close(); err != nil {
				logger.Error("failed to close reference data connection", "error", err)
			}
		}
	}()

	// Create gRPC service
	positionKeepingService, err := service.NewPositionKeepingService(
		container.PositionLogRepository,
		container.MeasurementRepository,
		container.EventPublisher,
		idempotencySvc,
		container.OutboxPublisher,
		serviceOpts...,
	)
	if err != nil {
		return fmt.Errorf("failed to create position keeping service: %w", err)
	}

	logger.Info("position keeping service initialized")

	// Create gRPC server using GrpcServerBuilder for consistent interceptor ordering.
	// If tracing is disabled (no OTLP endpoint), create a no-op tracer for the builder.
	tracer := container.Tracer
	if tracer == nil {
		var tracerErr error
		tracer, tracerErr = pkobservability.NewTracer(ctx, pkobservability.TracerConfig{
			ServiceName:  "position-keeping",
			OTLPEndpoint: "localhost:4317",
			Enabled:      false,
		})
		if tracerErr != nil {
			return fmt.Errorf("failed to create no-op tracer: %w", tracerErr)
		}
	}

	builder := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithUnaryInterceptor(interceptors.MetricsInterceptor(grpcRequestsTotal, grpcRequestDuration))

	if container.AuthInterceptor != nil {
		builder = builder.WithAuthInterceptor(container.AuthInterceptor)
	} else {
		builder = builder.WithoutAuth()
	}

	grpcServer, err := builder.Build()
	if err != nil {
		return fmt.Errorf("failed to build grpc server: %w", err)
	}

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

	// Graceful shutdown (runs for both signal and error paths)
	logger.Info("shutting down servers...")

	// Cancel run-scoped context to stop lazy resolution goroutines.
	runCancel()

	// Shutdown outbox worker before stopping servers (worker must stop before producer closes).
	// The shutdown function arrives via channel once the goroutine has started the worker.
	select {
	case shutdownFn := <-outboxShutdownCh:
		logger.Info("stopping event outbox worker...")
		shutdownFn()
		logger.Info("event outbox worker stopped")
	default:
		// Worker never started (Kafka not resolved yet) - nothing to stop
	}

	// Close the lazily-resolved Kafka producer (flush buffered records).
	select {
	case cleanupFn := <-kafkaCleanupCh:
		cleanupFn()
	default:
		// Kafka never resolved - nothing to close
	}

	// Shutdown compaction worker
	if compactionWorker != nil {
		logger.Info("stopping compaction worker...")
		if compactionWorkerCancel != nil {
			compactionWorkerCancel() // Cancel context first to signal worker to stop
		}
		compactionWorker.Stop() // Blocks until current compaction iteration completes
		logger.Info("compaction worker stopped")
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

	return runErr
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

// currentAccountClientAdapter adapts the generated gRPC client to the service.CurrentAccountClient interface.
// This adapter is needed because the service package defines its own interface for testability,
// while the generated client has a different method signature.
type currentAccountClientAdapter struct {
	client currentaccountv1.CurrentAccountServiceClient
}

// RetrieveCurrentAccount implements service.CurrentAccountClient by delegating to the generated client.
func (a *currentAccountClientAdapter) RetrieveCurrentAccount(ctx context.Context, req *currentaccountv1.RetrieveCurrentAccountRequest) (*currentaccountv1.RetrieveCurrentAccountResponse, error) {
	return a.client.RetrieveCurrentAccount(ctx, req)
}

// internalAccountClientAdapter adapts the generated gRPC client to the service.InternalAccountClient interface.
// This adapter is needed because the service package defines its own interface for testability,
// while the generated client has a different method signature.
type internalAccountClientAdapter struct {
	client internalaccountv1.InternalAccountServiceClient
}

// RetrieveInternalAccount implements service.InternalAccountClient by delegating to the generated client.
func (a *internalAccountClientAdapter) RetrieveInternalAccount(ctx context.Context, req *internalaccountv1.RetrieveInternalAccountRequest) (*internalaccountv1.RetrieveInternalAccountResponse, error) {
	return a.client.RetrieveInternalAccount(ctx, req)
}
