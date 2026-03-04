// Package main is the entry point for the financial-gateway standalone binary.
//
// It wires all financial-gateway components: gRPC service, Stripe adapter,
// HTTP webhook receiver, outbox worker, platform bootstrap, and health checks.
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

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	financialgatewayv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_gateway/v1"
	webhookhttp "github.com/meridianhub/meridian/services/financial-gateway/adapters/http"
	stripeadapter "github.com/meridianhub/meridian/services/financial-gateway/adapters/stripe"
	"github.com/meridianhub/meridian/services/financial-gateway/config"
	"github.com/meridianhub/meridian/services/financial-gateway/service"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// ErrMissingDatabaseURL is returned when the DATABASE_URL environment variable is not set.
var ErrMissingDatabaseURL = errors.New("DATABASE_URL is required")

// ErrMissingStripeAPIKey is returned when the STRIPE_SECRET_KEY environment variable is not set.
var ErrMissingStripeAPIKey = errors.New("STRIPE_SECRET_KEY is required")

// Build information set via ldflags during compilation.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Verify at compile time that *events.OutboxPublisher satisfies the webhook handler interface.
var _ webhookhttp.OutboxEventPublisher = (*events.OutboxPublisher)(nil)

func main() {
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting financial-gateway service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

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
	ctx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	cfg := config.LoadConfig()

	// Initialize OpenTelemetry tracer.
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "financial-gateway-service",
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// Initialize database connection.
	if cfg.DatabaseURL == "" {
		return bootstrap.Permanent(ErrMissingDatabaseURL)
	}

	dbCfg := bootstrap.DefaultDatabaseConfig()
	dbCfg.DSN = cfg.DatabaseURL
	dbCfg.Logger = logger

	db, err := bootstrap.NewDatabase(ctx, dbCfg)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer bootstrap.CloseDatabase(db, logger)

	logger.Info("database connection established")

	// Initialize auth interceptor.
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server.
	grpcServer := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()

	// Initialize and register FinancialGatewayService.
	svcCfg := service.Config{
		Logger: logger,
	}

	gatewaySvc, err := service.NewFinancialGatewayService(svcCfg)
	if err != nil {
		return fmt.Errorf("failed to create financial gateway service: %w", err)
	}
	financialgatewayv1.RegisterFinancialGatewayServiceServer(grpcServer, gatewaySvc)

	// Register health check.
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection for debugging.
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Create gRPC listener before serving to fail fast if port is unavailable.
	grpcAddress := fmt.Sprintf(":%s", cfg.GRPCPort)
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", grpcAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", grpcAddress, err)
	}
	// Close the listener if any subsequent setup fails (before grpcServer.Serve takes ownership).
	listenerClosed := false
	defer func() {
		if !listenerClosed {
			_ = listener.Close()
		}
	}()

	// --- Stripe webhook receiver setup ---

	// Require Stripe API key for the client factory.
	if cfg.StripeSecretKey == "" {
		return bootstrap.Permanent(ErrMissingStripeAPIKey)
	}

	// Build the per-tenant Stripe config provider.
	// When CONTROL_PLANE_ADDR is set, use ManifestTenantConfigProvider to fetch
	// per-tenant webhook secrets from control-plane manifests.
	// Otherwise fall back to a single-tenant env-var provider for local dev.
	tenantConfigProvider, controlPlaneConn, err := createTenantConfigProvider(cfg, logger)
	if err != nil {
		return fmt.Errorf("failed to create tenant config provider: %w", err)
	}
	if controlPlaneConn != nil {
		defer func() {
			if closeErr := controlPlaneConn.Close(); closeErr != nil {
				logger.Error("failed to close control-plane connection", "error", closeErr)
			}
		}()
	}

	stripeCfg := stripeadapter.DefaultConfig()
	stripeCfg.APIKey = cfg.StripeSecretKey

	clientFactory, err := stripeadapter.NewClientFactory(stripeCfg, tenantConfigProvider, logger)
	if err != nil {
		return fmt.Errorf("failed to create stripe client factory: %w", err)
	}

	// Initialize the outbox publisher and worker for webhook domain events.
	// Events written to the outbox within the same DB transaction are published
	// to Kafka asynchronously by the background worker.
	outboxPublisher := events.NewOutboxPublisher("financial-gateway")

	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers == "" {
		bootstrapServers = env.GetEnvOrDefault("KAFKA_BROKERS", "")
	}
	if bootstrapServers != "" {
		outboxRepo := events.NewPostgresOutboxRepository(db)
		producer, kafkaErr := kafka.NewProtoProducer(kafka.ProducerConfig{
			BootstrapServers: bootstrapServers,
			ClientID:         "financial-gateway-outbox-worker",
			Acks:             "all",
			Retries:          3,
			Compression:      "snappy",
		})
		if kafkaErr != nil {
			logger.Warn("failed to create Kafka producer for outbox worker", "error", kafkaErr)
		} else {
			defer producer.Close()
			workerConfig := events.DefaultWorkerConfig("financial-gateway")
			outboxWorker := events.NewWorker(outboxRepo, producer, workerConfig, logger)
			outboxWorker.Start(ctx)
			defer outboxWorker.Stop()
			logger.Info("outbox worker started", "bootstrap_servers", bootstrapServers)
		}
	} else {
		logger.Warn("outbox worker disabled - KAFKA_BOOTSTRAP_SERVERS not set (events will accumulate in outbox)")
	}

	// Create the webhook handler.
	// The handler validates Stripe-Signature, maps events to domain protos,
	// and publishes them to the transactional outbox.
	webhookHandler := webhookhttp.NewWebhookHandler(webhookhttp.WebhookHandlerConfig{
		ClientFactory:   clientFactory,
		OutboxPublisher: outboxPublisher,
		DB:              db,
		Logger:          logger,
	})

	// Create HTTP mux and register the Stripe webhook endpoint.
	mux := http.NewServeMux()
	// Tenant ID is embedded in the URL path so Stripe can be configured to call
	// per-tenant endpoints (e.g. /webhooks/stripe/acme-corp). The handler
	// extracts the tenant from r.PathValue("tenantID") and injects it into ctx.
	mux.HandleFunc("POST /webhooks/stripe/{tenantID}", webhookHandler.HandleStripeWebhook)

	httpAddress := fmt.Sprintf(":%s", cfg.HTTPPort)
	httpServer := &http.Server{
		Addr:              httpAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Channel to collect server errors.
	serverErrors := make(chan error, 2)

	// Start gRPC server in background.
	// grpcServer.Serve takes ownership of the listener; mark it so the deferred
	// close does not double-close on normal shutdown.
	listenerClosed = true
	go func() {
		logger.Info("starting gRPC server", "address", grpcAddress)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()

	// Start HTTP webhook server in background.
	go func() {
		logger.Info("starting HTTP webhook server", "address", httpAddress)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Wait for shutdown signal.
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)
	orchestrator.AddCleanup(func() error {
		runCancel()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), defaults.DefaultGracefulShutdown)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("failed to shutdown HTTP server", "error", err)
		}
		return nil
	})

	return orchestrator.Wait(serverErrors)
}

// createTenantConfigProvider builds the TenantConfigProvider.
// When CONTROL_PLANE_ADDR is set, connects to the control-plane gRPC service to read
// per-tenant Stripe manifest config. Otherwise falls back to an env-var provider.
func createTenantConfigProvider(cfg config.Config, logger *slog.Logger) (stripeadapter.TenantConfigProvider, *grpc.ClientConn, error) {
	if cfg.ControlPlaneAddr == "" {
		logger.Warn("CONTROL_PLANE_ADDR not set - using env-based single-tenant Stripe config")
		provider := &envTenantConfigProvider{
			webhookSecret: env.GetEnvOrDefault("STRIPE_WEBHOOK_SECRET", ""),
			accountID:     env.GetEnvOrDefault("STRIPE_CONNECTED_ACCOUNT_ID", ""),
		}
		return provider, nil, nil
	}

	// insecure.NewCredentials() is intentional: all inter-service gRPC traffic
	// runs inside the cluster and is secured at the network layer (mTLS via
	// the service mesh / Kubernetes CNI). Application-layer TLS is not used
	// for internal service-to-service calls across the Meridian platform.
	conn, err := grpc.NewClient(
		cfg.ControlPlaneAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to dial control-plane at %s: %w", cfg.ControlPlaneAddr, err)
	}

	manifestClient := controlplanev1.NewManifestHistoryServiceClient(conn)
	provider, err := stripeadapter.NewManifestTenantConfigProvider(stripeadapter.ManifestTenantConfigProviderConfig{
		Client: manifestClient,
		Logger: logger,
	})
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("failed to create manifest tenant config provider: %w", err)
	}

	logger.Info("using control-plane manifest for per-tenant Stripe config", "addr", cfg.ControlPlaneAddr)
	return provider, conn, nil
}

// envTenantConfigProvider is a single-tenant fallback that reads Stripe config from
// environment variables. Used in local development when no control-plane is available.
type envTenantConfigProvider struct {
	webhookSecret string
	accountID     string
}

func (p *envTenantConfigProvider) GetTenantConfig(_ string) (stripeadapter.TenantConfig, error) {
	if p.accountID == "" {
		return stripeadapter.TenantConfig{}, stripeadapter.ErrTenantConfigNotFound
	}
	return stripeadapter.TenantConfig{
		ConnectedAccountID:    p.accountID,
		WebhookEndpointSecret: p.webhookSecret,
	}, nil
}

// parseLogLevel converts a string log level to slog.Level.
func parseLogLevel(levelStr string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(levelStr)) {
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
