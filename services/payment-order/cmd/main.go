// Package main is the entry point for the PaymentOrder service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"sync"

	billingpb "github.com/meridianhub/meridian/api/proto/meridian/billing/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	webhookhttp "github.com/meridianhub/meridian/services/payment-order/adapters/http"
	pomessaging "github.com/meridianhub/meridian/services/payment-order/adapters/messaging"
	"github.com/meridianhub/meridian/services/payment-order/app"
	"github.com/meridianhub/meridian/services/payment-order/config"
	"github.com/meridianhub/meridian/services/payment-order/service"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Build information set via ldflags during compilation.
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

	logger.Info("starting payment-order service",
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

// paymentOrderServers holds the initialized servers and consumer for the run lifecycle.
type paymentOrderServers struct {
	grpcServer           *grpc.Server
	grpcListener         net.Listener
	httpServer           *webhookhttp.Server
	paymentEventConsumer *pomessaging.PaymentEventConsumer
}

func run(logger *slog.Logger) error {
	ctx := context.Background()

	// Load and validate service configuration early (permanent error if invalid)
	svcConfig := config.LoadServiceConfig()
	if err := svcConfig.Validate(); err != nil {
		return bootstrap.Permanent(fmt.Errorf("invalid service configuration: %w", err))
	}
	svcConfig.LogValues(logger)

	// Initialize dependency container
	container, err := app.NewContainer(ctx, &svcConfig, logger, Version)
	if err != nil {
		return err
	}
	defer container.Close()

	// Create services and servers
	paymentOrderService, err := createPaymentOrderService(container, &svcConfig, logger)
	if err != nil {
		return err
	}

	servers, err := setupServers(ctx, container, &svcConfig, paymentOrderService, logger)
	if err != nil {
		return err
	}
	if servers.paymentEventConsumer != nil {
		defer func() {
			if err := servers.paymentEventConsumer.Close(); err != nil {
				logger.Error("failed to close payment event consumer", "error", err)
			}
		}()
	}

	// Start all servers and workers, then wait for shutdown
	return startAndAwaitShutdown(ctx, &svcConfig, container, servers, logger)
}

// createPaymentOrderService creates the main payment order service with all dependencies.
func createPaymentOrderService(container *app.Container, svcConfig *config.ServiceConfig, logger *slog.Logger) (*service.Service, error) {
	paymentOrderService, err := service.NewServiceWithConfig(service.Config{
		Repository:                container.PaymentOrderRepo,
		CurrentAccountClient:      container.CurrentAccountClient,
		FinancialAccountingClient: container.FinancialAccountingClient,
		InternalAccountClient:     container.InternalAccountClient,
		ReferenceDataClient:       container.ReferenceDataClient,
		PaymentGateway:            container.PaymentGateway,
		GatewayAccountConfig:      container.GatewayAccountConfig,
		KafkaPublisher:            container.EventPublisher,
		IdempotencyService:        container.IdempotencyService,
		Logger:                    logger,
		Tracer:                    container.Tracer,
		InternalClearingEnabled:   container.InternalClearingEnabled,
		HandlerRegistry:           container.HandlerRegistry,
		SagaExecutionLogger:       container.SagaExecutionRepo,
		SagaOrchestrationEnabled:  svcConfig.SagaOrchestrationEnabled,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create payment order service: %w", err)
	}

	logger.Info("saga orchestration configuration",
		"saga_orchestration_enabled", svcConfig.SagaOrchestrationEnabled)

	return paymentOrderService, nil
}

// setupServers creates gRPC, HTTP, and Kafka consumer servers.
func setupServers(_ context.Context, container *app.Container, svcConfig *config.ServiceConfig, paymentOrderService *service.Service, logger *slog.Logger) (*paymentOrderServers, error) {
	grpcServer, err := bootstrap.NewGrpcServerBuilder(container.Tracer, logger).
		WithAuthInterceptor(container.AuthInterceptor).
		Build() //nolint:contextcheck // gRPC interceptors manage their own contexts
	if err != nil {
		return nil, fmt.Errorf("failed to build grpc server: %w", err)
	}

	pb.RegisterPaymentOrderServiceServer(grpcServer, paymentOrderService)
	billingGRPCService := service.NewBillingService(container.BillingRepo, nil, logger)
	billingpb.RegisterBillingServiceServer(grpcServer, billingGRPCService)
	grpc_health_v1.RegisterHealthServer(grpcServer, &simpleHealthServer{})
	reflection.Register(grpcServer)
	logger.Info("gRPC services registered")

	localServiceClient := &localPaymentOrderClient{service: paymentOrderService}
	httpServer, err := createWebhookHTTPServer(localServiceClient, logger)
	if err != nil {
		return nil, err
	}

	grpcListener, err := createGRPCListener() //nolint:contextcheck // listener intentionally outlives request contexts
	if err != nil {
		return nil, err
	}

	paymentEventConsumer, err := createPaymentEventConsumer(container, localServiceClient, grpcListener, logger)
	if err != nil {
		return nil, err
	}

	_ = svcConfig // used in caller for billing config
	return &paymentOrderServers{
		grpcServer:           grpcServer,
		grpcListener:         grpcListener,
		httpServer:           httpServer,
		paymentEventConsumer: paymentEventConsumer,
	}, nil
}

// createWebhookHTTPServer creates the HTTP webhook server with HMAC authentication.
func createWebhookHTTPServer(localClient *localPaymentOrderClient, logger *slog.Logger) (*webhookhttp.Server, error) {
	hmacSecret := []byte(env.GetEnvOrDefault("WEBHOOK_HMAC_SECRET", ""))
	if len(hmacSecret) == 0 {
		return nil, bootstrap.Permanent(app.ErrMissingHMACSecret)
	}
	webhookHandler, err := webhookhttp.NewWebhookHandler(webhookhttp.WebhookHandlerConfig{
		PaymentOrderService: localClient,
		HMACSecret:          hmacSecret,
		Logger:              logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create webhook handler: %w", err)
	}
	httpPort := env.GetEnvAsInt("HTTP_PORT", ports.Gateway)
	return webhookhttp.NewServer(webhookhttp.ServerConfig{
		Port:               httpPort,
		WebhookHandler:     webhookHandler,
		Logger:             logger,
		RateLimitPerSecond: env.GetEnvAsFloat("HTTP_RATE_LIMIT_PER_SECOND", 100),
		RateLimitBurst:     env.GetEnvAsInt("HTTP_RATE_LIMIT_BURST", 200),
	})
}

// createGRPCListener creates the TCP listener for the gRPC server.
func createGRPCListener() (net.Listener, error) {
	grpcPort := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.PaymentOrder))
	grpcAddress := fmt.Sprintf(":%s", grpcPort)
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", grpcAddress)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", grpcAddress, err)
	}
	return listener, nil
}

// createPaymentEventConsumer creates the Kafka payment event consumer if bootstrap servers are configured.
func createPaymentEventConsumer(container *app.Container, localClient *localPaymentOrderClient, grpcListener net.Listener, logger *slog.Logger) (*pomessaging.PaymentEventConsumer, error) {
	if container.BootstrapServers == "" {
		logger.Warn("payment event consumer disabled - KAFKA_BOOTSTRAP_SERVERS not set")
		return nil, nil //nolint:nilnil // nil consumer is a valid "disabled" state
	}
	consumer, err := pomessaging.NewPaymentEventConsumerWithKafka(
		kafka.ConsumerConfig{
			BootstrapServers: container.BootstrapServers,
			GroupID:          "payment-order-gateway-events",
			ClientID:         "payment-order-gateway-events",
			AutoOffsetReset:  "earliest",
			EnableAutoCommit: false,
		},
		localClient,
		logger,
	)
	if err != nil {
		_ = grpcListener.Close()
		return nil, fmt.Errorf("failed to create payment event consumer: %w", err)
	}
	return consumer, nil
}

// startAndAwaitShutdown starts all servers and workers, waits for shutdown, and cleans up.
func startAndAwaitShutdown(ctx context.Context, svcConfig *config.ServiceConfig, container *app.Container, servers *paymentOrderServers, logger *slog.Logger) error {
	serverErrors := make(chan error, 3)
	launchServers(servers, serverErrors, logger) //nolint:contextcheck // servers manage their own goroutine lifecycles

	// Start billing workers in background (if enabled)
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	billingWg := startBillingWorkers(workerCtx, svcConfig, container, logger)

	// Wait for interrupt signal or server error
	runErr := awaitSignalOrError(serverErrors, logger)

	logger.Info("shutting down servers...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaults.DefaultGracefulShutdown)
	defer cancel()

	stopBillingWorkers(svcConfig, container, workerCancel, billingWg, logger)
	if servers.paymentEventConsumer != nil {
		servers.paymentEventConsumer.Stop()
		logger.Info("payment event consumer stopped")
	}
	if err := servers.httpServer.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck // shutdown uses dedicated timeout context
		logger.Error("failed to shutdown HTTP server", "error", err)
	}
	gracefulStopGRPC(shutdownCtx, servers.grpcServer, logger) //nolint:contextcheck // shutdown uses dedicated timeout context
	return runErr
}

// launchServers starts gRPC, HTTP, and payment event consumer servers in background goroutines.
func launchServers(servers *paymentOrderServers, serverErrors chan error, logger *slog.Logger) {
	go func() {
		logger.Info("starting gRPC server", "address", servers.grpcListener.Addr().String())
		if err := servers.grpcServer.Serve(servers.grpcListener); err != nil {
			serverErrors <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()
	go func() {
		logger.Info("starting HTTP server")
		if err := servers.httpServer.Start(); err != nil {
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()
	if servers.paymentEventConsumer != nil {
		go func() {
			if err := servers.paymentEventConsumer.Start(
				"financial-gateway.payment-captured.v1",
				"financial-gateway.payment-failed.v1",
			); err != nil {
				logger.Error("payment event consumer error", "error", err)
				serverErrors <- fmt.Errorf("payment event consumer error: %w", err)
			}
		}()
	}
}

// startBillingWorkers starts billing scheduler and dunning worker if enabled.
func startBillingWorkers(ctx context.Context, svcConfig *config.ServiceConfig, container *app.Container, logger *slog.Logger) *sync.WaitGroup {
	var wg sync.WaitGroup
	if !svcConfig.BillingEnabled || container.BillingCronScheduler == nil || container.DunningWorker == nil {
		return &wg
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := container.BillingCronScheduler.Start(ctx); err != nil {
			logger.Error("billing scheduler error", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := container.DunningWorker.Start(ctx); err != nil {
			logger.Error("dunning worker error", "error", err)
		}
	}()
	logger.Info("billing workers started")
	return &wg
}

// awaitSignalOrError blocks until a shutdown signal or server error is received.
func awaitSignalOrError(serverErrors chan error, logger *slog.Logger) error {
	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()
	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
		return nil
	case err := <-serverErrors:
		logger.Error("server error", "error", err)
		select {
		case sig := <-sigChan:
			logger.Info("received signal during error handling, treating as shutdown", "signal", sig)
			return nil
		default:
		}
		return err
	}
}

// stopBillingWorkers stops billing scheduler and dunning worker, waiting for completion.
func stopBillingWorkers(svcConfig *config.ServiceConfig, container *app.Container, workerCancel context.CancelFunc, wg *sync.WaitGroup, logger *slog.Logger) {
	if !svcConfig.BillingEnabled || container.BillingCronScheduler == nil || container.DunningWorker == nil {
		return
	}
	logger.Info("stopping billing workers...")
	workerCancel()
	container.BillingCronScheduler.Stop()
	container.DunningWorker.Stop()
	wg.Wait()
	logger.Info("billing workers stopped")
}

// gracefulStopGRPC attempts a graceful gRPC server stop with timeout fallback.
func gracefulStopGRPC(shutdownCtx context.Context, grpcServer *grpc.Server, logger *slog.Logger) {
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
		logger.Info("servers stopped gracefully")
	case <-shutdownCtx.Done():
		logger.Warn("graceful shutdown timeout, forcing stop")
		grpcServer.Stop()
	}
}
