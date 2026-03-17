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
	"github.com/meridianhub/meridian/shared/platform/ports"
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

func run(logger *slog.Logger) error {
	ctx := context.Background()

	// Initialize OpenTelemetry tracer
	tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
		ServiceName:    "party-service",
		ServiceVersion: Version,
		Logger:         logger,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer bootstrap.ShutdownTracer(tracer, logger)

	// Initialize database connection
	dbConfig := bootstrap.DefaultDatabaseConfig()
	dbConfig.Logger = logger
	db, err := bootstrap.NewDatabase(ctx, dbConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}

	logger.Info("database connection established")

	// Create repositories
	repo := persistence.NewRepository(db)
	pmRepo := persistence.NewPaymentMethodRepository(db)
	partyTypeRepo := persistence.NewPartyTypeDefinitionRepository(db)
	outboxRepo := events.NewPostgresOutboxRepository(db)

	// Create outbox publisher for event publishing
	outboxPublisher := events.NewOutboxPublisher("party")

	// Create party service
	partyService, err := service.NewService(repo, logger)
	if err != nil {
		return fmt.Errorf("failed to create party service: %w", err)
	}
	partyService.WithPaymentMethodRepository(pmRepo)
	partyService.WithOutboxPublisher(outboxPublisher, db)

	// Create and wire party type definition service
	partyTypeSvc, err := service.NewPartyTypeDefinitionService(partyTypeRepo)
	if err != nil {
		return fmt.Errorf("failed to create party type definition service: %w", err)
	}
	partyService.WithPartyTypeDefinitionService(partyTypeSvc)

	// Create and wire attribute validator
	attributeValidator, err := service.NewAttributeValidator(partyTypeRepo, partyTypeSvc.CELCompiler())
	if err != nil {
		return fmt.Errorf("failed to create attribute validator: %w", err)
	}
	partyService.WithAttributeValidator(attributeValidator)

	// Load verification config with environment-aware validation.
	// Production: fail fast if config is missing or invalid.
	// Development: allow service to start without provider (warn and continue).
	environment := env.GetEnvOrDefault("ENVIRONMENT", "development")
	isProduction := environment == "production" || environment == "prod"

	verificationCfg, err := config.LoadVerificationConfig()
	if err != nil {
		if isProduction {
			return bootstrap.Permanent(fmt.Errorf("verification config required in production: %w", err))
		}
		logger.Warn("verification config not loaded - KYC provider disabled", "error", err)
		verificationCfg = nil
	}
	if verificationCfg != nil {
		if err := verificationCfg.ValidateForEnvironment(environment); err != nil {
			return bootstrap.Permanent(fmt.Errorf("verification config invalid for environment %q: %w", environment, err))
		}
	}

	// Create verification provider and service when configured
	var provider verification.Provider
	var verificationSvc *service.VerificationService
	var verificationRepo *persistence.VerificationRepository
	if verificationCfg != nil {
		provider, err = verification.NewProvider(verificationCfg)
		if err != nil {
			return fmt.Errorf("failed to create verification provider: %w", err)
		}
		logger.Info("verification provider initialized", "provider", verificationCfg.Provider)

		partyService.WithVerificationProvider(provider)

		verificationRepo = persistence.NewVerificationRepository(db)
		verificationEventPublisher := service.NewOutboxVerificationEventPublisher(outboxPublisher, db)
		verificationSvc, err = service.NewVerificationService(
			&partyRepoAdapter{repo: repo},
			verificationRepo,
			provider,
			verificationEventPublisher,
			logger,
		)
		if err != nil {
			return fmt.Errorf("failed to create verification service: %w", err)
		}
	}

	logger.Info("party service initialized")

	// Start outbox worker for Kafka event delivery (optional - depends on KAFKA_BOOTSTRAP_SERVERS)
	var outboxWorkerStop func()
	bootstrapServers := env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", "")
	if bootstrapServers != "" {
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
		} else {
			w := events.NewWorker(outboxRepo, producer, events.DefaultWorkerConfig("party"), logger)
			w.Start(ctx)
			outboxWorkerStop = w.Stop
			logger.Info("outbox worker started")
		}
	} else {
		logger.Warn("KAFKA_BOOTSTRAP_SERVERS not configured, outbox worker disabled - events will be persisted but not published")
	}

	// Initialize auth interceptor (optional - based on AUTH_ENABLED)
	authConfig := bootstrap.DefaultAuthConfig(logger)
	authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server with interceptor chain
	// Order is handled by bootstrap: tracing -> auth -> recovery
	grpcServer, err := bootstrap.NewGrpcServerBuilder(tracer, logger).
		WithAuthInterceptor(authInterceptor).
		Build()
	if err != nil {
		return fmt.Errorf("failed to build grpc server: %w", err)
	}

	// Register services
	pb.RegisterPartyServiceServer(grpcServer, partyService)

	// Register health check service
	healthChecker := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:   repo,
		Logger:       logger,
		ServiceName:  "party",
		CheckTimeout: defaults.DefaultHealthCheckTimeout,
	})
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get port from environment (default is centralized in shared/platform/ports)
	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.Party))
	address := fmt.Sprintf(":%s", port)

	// Create listener
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	// Start gRPC server in background
	serverErrors := make(chan error, 2)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Wait for shutdown signal and orchestrate graceful shutdown
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)

	// Start timeout handler and HTTP server when verification is configured
	if verificationSvc != nil && verificationCfg != nil {
		timeoutCtx, timeoutCancel := context.WithCancel(context.Background())
		timeoutHandler, err := verification.NewTimeoutHandler(verification.TimeoutHandlerConfig{
			VerificationRepo: verificationRepo,
			Provider:         provider,
			Logger:           logger,
		})
		if err != nil {
			timeoutCancel()
			return fmt.Errorf("failed to create timeout handler: %w", err)
		}
		go timeoutHandler.Run(timeoutCtx)

		orchestrator.AddCleanup(func() error {
			timeoutCancel()
			return nil
		})
		httpMux := http.NewServeMux()

		webhookHandler, err := httpAdapter.NewVerificationWebhookHandler(
			httpAdapter.VerificationWebhookHandlerConfig{
				VerificationService: verificationSvc,
				HMACSecrets: map[string][]byte{
					"default": []byte(verificationCfg.WebhookSecret),
					"stripe":  []byte(verificationCfg.WebhookSecret),
				},
				Logger: logger,
			},
		)
		if err != nil {
			return fmt.Errorf("failed to create webhook handler: %w", err)
		}

		// Register provider-specific webhook routes before the generic catch-all.
		// For Stripe, we use the StripeWebhookAdapter which validates the Stripe-Signature
		// header (using the Stripe endpoint signing secret) and translates the Stripe event
		// format to our generic webhook format (signed with the generic HMAC secret).
		if strings.ToLower(verificationCfg.Provider) == "stripe" {
			stripeAdapter, err := httpAdapter.NewStripeWebhookAdapter(
				httpAdapter.StripeWebhookAdapterConfig{
					InnerHandler:    webhookHandler,
					WebhookSecret:   []byte(verificationCfg.StripeWebhookSecret),
					InnerHMACSecret: []byte(verificationCfg.WebhookSecret),
					Logger:          logger,
				},
			)
			if err != nil {
				return bootstrap.Permanent(fmt.Errorf("failed to create stripe webhook adapter: %w", err))
			}
			httpMux.Handle("/webhooks/verification/stripe", stripeAdapter)
		}

		httpMux.HandleFunc("/webhooks/verification/", webhookHandler.HandleWebhook)
		httpMux.HandleFunc("/health", newHTTPHealthHandler(verificationCfg))

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
