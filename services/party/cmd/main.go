// Package main is the entry point for the Party service.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	httpAdapter "github.com/meridianhub/meridian/services/party/adapters/http"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/config"
	"github.com/meridianhub/meridian/services/party/domain"
	"github.com/meridianhub/meridian/services/party/service"
	"github.com/meridianhub/meridian/services/party/verification"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"gorm.io/gorm"
)

// Build information set via ldflags during compilation
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	// Initialize structured logging with configurable log level
	// Note: bootstrap.NewLogger hardcodes INFO level, so we create logger manually for LOG_LEVEL support
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting party service",
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

// partyDeps holds initialized services and dependencies for the party service.
type partyDeps struct {
	partyService     *service.Service
	repo             *persistence.Repository
	outboxRepo       *events.PostgresOutboxRepository
	verificationSvc  *service.VerificationService
	verificationCfg  *config.VerificationConfig
	verificationRepo *persistence.VerificationRepository
	provider         verification.Provider
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	// Initialize tracer and database
	tracer, db, err := initInfra(ctx, logger)
	if err != nil {
		return err
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// Create services and dependencies
	deps, err := initPartyServices(db, logger)
	if err != nil {
		return err
	}

	// Start outbox worker for Kafka event delivery (optional)
	outboxWorkerStop := initOutboxWorker(ctx, deps.outboxRepo, logger)

	// Create gRPC server, register services, and start listening
	grpcServer, listener, err := setupGRPCServer(ctx, tracer, logger, deps)
	if err != nil {
		return err
	}

	serverErrors := make(chan error, 2)
	go func() {
		logger.Info("starting gRPC server", "address", listener.Addr().String())
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Wait for shutdown signal and orchestrate graceful shutdown
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)

	// Start verification HTTP server if configured
	if deps.verificationSvc != nil && deps.verificationCfg != nil {
		if err := startVerificationHTTPServer(deps, orchestrator, serverErrors, logger); err != nil {
			return err
		}
	}

	if outboxWorkerStop != nil {
		orchestrator.AddCleanup(func() error {
			outboxWorkerStop()
			return nil
		})
	}

	orchestrator.AddCleanup(func() error {
		bootstrap.CloseDatabase(db, logger)
		return nil
	})

	return orchestrator.Wait(serverErrors)
}

// initPartyServices creates all repositories, services, and optional verification components.
func initPartyServices(db *gorm.DB, logger *slog.Logger) (*partyDeps, error) {
	repo := persistence.NewRepository(db)
	pmRepo := persistence.NewPaymentMethodRepository(db)
	partyTypeRepo := persistence.NewPartyTypeDefinitionRepository(db)
	outboxRepo := events.NewPostgresOutboxRepository(db)

	outboxPublisher := events.NewOutboxPublisher("party")

	partyService, err := service.NewService(repo, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create party service: %w", err)
	}
	partyService.WithPaymentMethodRepository(pmRepo)
	partyService.WithOutboxPublisher(outboxPublisher, db)

	partyTypeSvc, err := service.NewPartyTypeDefinitionService(partyTypeRepo)
	if err != nil {
		return nil, fmt.Errorf("failed to create party type definition service: %w", err)
	}
	partyService.WithPartyTypeDefinitionService(partyTypeSvc)

	attributeValidator, err := service.NewAttributeValidator(partyTypeRepo, partyTypeSvc.CELCompiler())
	if err != nil {
		return nil, fmt.Errorf("failed to create attribute validator: %w", err)
	}
	partyService.WithAttributeValidator(attributeValidator)

	// Verification setup (optional, environment-aware)
	verificationCfg, provider, verificationSvc, verificationRepo, err := initVerification(db, repo, outboxPublisher, logger)
	if err != nil {
		return nil, err
	}
	if provider != nil {
		partyService.WithVerificationProvider(provider)
	}

	logger.Info("party service initialized")

	return &partyDeps{
		partyService:     partyService,
		repo:             repo,
		outboxRepo:       outboxRepo,
		verificationSvc:  verificationSvc,
		verificationCfg:  verificationCfg,
		verificationRepo: verificationRepo,
		provider:         provider,
	}, nil
}

// initVerification loads verification config and creates provider/service if configured.
func initVerification(db *gorm.DB, repo *persistence.Repository, outboxPublisher *events.OutboxPublisher, logger *slog.Logger) (*config.VerificationConfig, verification.Provider, *service.VerificationService, *persistence.VerificationRepository, error) {
	environment := env.GetEnvOrDefault("ENVIRONMENT", "development")
	isProduction := environment == "production" || environment == "prod"

	verificationCfg, err := config.LoadVerificationConfig()
	if err != nil {
		if isProduction {
			return nil, nil, nil, nil, bootstrap.Permanent(fmt.Errorf("verification config required in production: %w", err))
		}
		logger.Warn("verification config not loaded - KYC provider disabled", "error", err)
		return nil, nil, nil, nil, nil
	}

	if err := verificationCfg.ValidateForEnvironment(environment); err != nil {
		return nil, nil, nil, nil, bootstrap.Permanent(fmt.Errorf("verification config invalid for environment %q: %w", environment, err))
	}

	provider, err := verification.NewProvider(verificationCfg)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to create verification provider: %w", err)
	}
	logger.Info("verification provider initialized", "provider", verificationCfg.Provider)

	verificationRepo := persistence.NewVerificationRepository(db)
	verificationEventPublisher := service.NewOutboxVerificationEventPublisher(outboxPublisher, db)
	verificationSvc, err := service.NewVerificationService(
		&partyRepoAdapter{repo: repo},
		verificationRepo,
		provider,
		verificationEventPublisher,
		logger,
	)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("failed to create verification service: %w", err)
	}

	return verificationCfg, provider, verificationSvc, verificationRepo, nil
}

// initInfra initializes the OpenTelemetry tracer and database connection.
func initInfra(ctx context.Context, logger *slog.Logger) (*observability.Tracer, *gorm.DB, error) {
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "party-service",
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize tracer: %w", err)
	}

	dbConfig := bootstrap.DefaultDatabaseConfig()
	dbConfig.Logger = logger
	db, err := bootstrap.NewDatabase(ctx, dbConfig)
	if err != nil {
		bootstrap.ShutdownTracer(tracer, logger) //nolint:contextcheck // ShutdownTracer manages its own context
		return nil, nil, fmt.Errorf("failed to initialize database: %w", err)
	}
	logger.Info("database connection established")

	return tracer, db, nil
}

// initOutboxWorker starts the Kafka outbox worker if KAFKA_BOOTSTRAP_SERVERS is configured.
func initOutboxWorker(ctx context.Context, outboxRepo *events.PostgresOutboxRepository, logger *slog.Logger) func() {
	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers == "" {
		logger.Warn("KAFKA_BOOTSTRAP_SERVERS not configured, outbox worker disabled - events will be persisted but not published")
		return nil
	}

	producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: bootstrapServers,
		ClientID:         "party-outbox-worker",
		Acks:             "all",
		Retries:          3,
		Compression:      "snappy",
	})
	if err != nil {
		logger.Warn("failed to create Kafka producer for outbox worker - events will be persisted but not published",
			"error", err)
		return nil
	}

	w := events.NewWorker(outboxRepo, producer, events.DefaultWorkerConfig("party"), logger)
	w.Start(ctx)
	logger.Info("outbox worker started")
	return w.Stop
}

// setupGRPCServer creates the gRPC server, registers services, and creates the TCP listener.
func setupGRPCServer(ctx context.Context, tracer *observability.Tracer, logger *slog.Logger, deps *partyDeps) (*grpc.Server, net.Listener, error) {
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize auth: %w", err)
	}

	grpcServer, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build() //nolint:contextcheck // gRPC interceptors manage their own contexts
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build grpc server: %w", err)
	}

	pb.RegisterPartyServiceServer(grpcServer, deps.partyService)

	healthChecker := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:   deps.repo,
		Logger:       logger,
		ServiceName:  "party",
		CheckTimeout: defaults.DefaultHealthCheckTimeout,
	})
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)
	reflection.Register(grpcServer)
	logger.Info("gRPC services registered")

	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.Party))
	address := fmt.Sprintf(":%s", port)
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address) //nolint:contextcheck // listener intentionally outlives request contexts
	if err != nil {
		return nil, nil, fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	return grpcServer, listener, nil
}

// startVerificationHTTPServer sets up the verification webhook HTTP server and registers
// cleanup handlers with the shutdown orchestrator.
func startVerificationHTTPServer(deps *partyDeps, orchestrator *bootstrap.ShutdownOrchestrator, serverErrors chan error, logger *slog.Logger) error {
	timeoutCtx, timeoutCancel := context.WithCancel(context.Background())
	timeoutHandler, err := verification.NewTimeoutHandler(verification.TimeoutHandlerConfig{
		VerificationRepo: deps.verificationRepo,
		Provider:         deps.provider,
		Logger:           logger,
	})
	if err != nil {
		timeoutCancel()
		return fmt.Errorf("failed to create timeout handler: %w", err)
	}
	go timeoutHandler.Run(timeoutCtx)
	orchestrator.AddCleanup(func() error { timeoutCancel(); return nil })

	httpMux, err := buildVerificationMux(deps, logger)
	if err != nil {
		return err
	}

	httpPort := env.GetEnvOrDefault("HTTP_PORT", "8081")
	httpServer := &http.Server{
		Addr:              ":" + httpPort,
		Handler:           httpMux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("starting HTTP server for webhooks", "port", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	orchestrator.AddCleanup(func() error {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	})
	return nil
}

// buildVerificationMux creates the HTTP mux with webhook and health handlers for verification.
func buildVerificationMux(deps *partyDeps, logger *slog.Logger) (*http.ServeMux, error) {
	webhookHandler, err := httpAdapter.NewVerificationWebhookHandler(
		httpAdapter.VerificationWebhookHandlerConfig{
			VerificationService: deps.verificationSvc,
			HMACSecrets: map[string][]byte{
				"default": []byte(deps.verificationCfg.WebhookSecret),
				"stripe":  []byte(deps.verificationCfg.WebhookSecret),
			},
			Logger: logger,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create webhook handler: %w", err)
	}

	httpMux := http.NewServeMux()
	if strings.ToLower(deps.verificationCfg.Provider) == "stripe" {
		stripeAdapter, err := httpAdapter.NewStripeWebhookAdapter(
			httpAdapter.StripeWebhookAdapterConfig{
				InnerHandler:    webhookHandler,
				WebhookSecret:   []byte(deps.verificationCfg.StripeWebhookSecret),
				InnerHMACSecret: []byte(deps.verificationCfg.WebhookSecret),
				Logger:          logger,
			},
		)
		if err != nil {
			return nil, bootstrap.Permanent(fmt.Errorf("failed to create stripe webhook adapter: %w", err))
		}
		httpMux.Handle("/webhooks/verification/stripe", stripeAdapter)
	}

	httpMux.HandleFunc("/webhooks/verification/", webhookHandler.HandleWebhook)
	httpMux.HandleFunc("/health", newHTTPHealthHandler(deps.verificationCfg))
	return httpMux, nil
}

// partyRepoAdapter adapts persistence.Repository to satisfy
// service.PartyRepository interface for the VerificationService.
type partyRepoAdapter struct {
	repo *persistence.Repository
}

func (a *partyRepoAdapter) FindByID(ctx context.Context, partyID uuid.UUID) (*domain.Party, error) {
	return a.repo.FindByID(ctx, partyID)
}

func (a *partyRepoAdapter) ExistsByID(ctx context.Context, partyID uuid.UUID) (bool, error) {
	return a.repo.ExistsByID(ctx, partyID)
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

// httpHealthResponse is the JSON structure returned by the HTTP health endpoint.
type httpHealthResponse struct {
	Status               string `json:"status"`
	Timestamp            string `json:"timestamp"`
	VerificationEnabled  bool   `json:"verification_enabled"`
	VerificationProvider string `json:"verification_provider,omitempty"`
}

// newHTTPHealthHandler returns an HTTP handler that reports service health
// including verification provider status.
func newHTTPHealthHandler(verificationCfg *config.VerificationConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		resp := httpHealthResponse{
			Status:    "ok",
			Timestamp: time.Now().UTC().Format(time.RFC3339),
		}
		if verificationCfg != nil {
			resp.VerificationEnabled = true
			resp.VerificationProvider = verificationCfg.Provider
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}
}
