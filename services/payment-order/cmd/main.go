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

	// Create payment order service
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
		return fmt.Errorf("failed to create payment order service: %w", err)
	}

	logger.Info("saga orchestration configuration",
		"saga_orchestration_enabled", svcConfig.SagaOrchestrationEnabled)

	// Create gRPC server with interceptor chain
	// Order is handled by bootstrap: tracing -> auth -> recovery
	grpcServer, err := bootstrap.NewGrpcServerBuilder(container.Tracer, logger).
		WithAuthInterceptor(container.AuthInterceptor).
		Build()
	if err != nil {
		return fmt.Errorf("failed to build grpc server: %w", err)
	}

	// Register gRPC services
	pb.RegisterPaymentOrderServiceServer(grpcServer, paymentOrderService)

	// Register billing gRPC service. Email outbox is nil until the email worker
	// is wired into this service; ResendInvoiceEmail returns Unavailable until then.
	billingGRPCService := service.NewBillingService(container.BillingRepo, nil /* emailRepo */, logger)
	billingpb.RegisterBillingServiceServer(grpcServer, billingGRPCService)

	grpc_health_v1.RegisterHealthServer(grpcServer, &simpleHealthServer{})
	reflection.Register(grpcServer)
	logger.Info("gRPC services registered")

	// Create HTTP webhook handler
	hmacSecret := []byte(env.GetEnvOrDefault("WEBHOOK_HMAC_SECRET", ""))
	if len(hmacSecret) == 0 {
		return bootstrap.Permanent(app.ErrMissingHMACSecret)
	}

	// Create a gRPC client wrapper for the local service
	localServiceClient := &localPaymentOrderClient{service: paymentOrderService}

	webhookHandler, err := webhookhttp.NewWebhookHandler(webhookhttp.WebhookHandlerConfig{
		PaymentOrderService: localServiceClient,
		HMACSecret:          hmacSecret,
		Logger:              logger,
	})
	if err != nil {
		return fmt.Errorf("failed to create webhook handler: %w", err)
	}

	// Create HTTP server
	httpPort := env.GetEnvAsInt("HTTP_PORT", ports.Gateway)
	httpServer, err := webhookhttp.NewServer(webhookhttp.ServerConfig{
		Port:               httpPort,
		WebhookHandler:     webhookHandler,
		Logger:             logger,
		RateLimitPerSecond: env.GetEnvAsFloat("HTTP_RATE_LIMIT_PER_SECOND", 100),
		RateLimitBurst:     env.GetEnvAsInt("HTTP_RATE_LIMIT_BURST", 200),
	})
	if err != nil {
		return fmt.Errorf("failed to create HTTP server: %w", err)
	}

	// Get gRPC port
	grpcPort := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.PaymentOrder))
	grpcAddress := fmt.Sprintf(":%s", grpcPort)

	// Create gRPC listener
	grpcListener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", grpcAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", grpcAddress, err)
	}

	// Construct the financial-gateway payment event consumer before starting servers
	// so that any initialization error causes an early return without leaving
	// running goroutines/listeners behind.
	var paymentEventConsumer *pomessaging.PaymentEventConsumer
	if container.BootstrapServers != "" {
		paymentEventConsumer, err = pomessaging.NewPaymentEventConsumerWithKafka(
			kafka.ConsumerConfig{
				BootstrapServers: container.BootstrapServers,
				GroupID:          "payment-order-gateway-events",
				ClientID:         "payment-order-gateway-events",
				AutoOffsetReset:  "earliest",
				EnableAutoCommit: false,
			},
			localServiceClient,
			logger,
		)
		if err != nil {
			return fmt.Errorf("failed to create payment event consumer: %w", err)
		}
		defer func() {
			if err := paymentEventConsumer.Close(); err != nil {
				logger.Error("failed to close payment event consumer", "error", err)
			}
		}()
	} else {
		logger.Warn("payment event consumer disabled - KAFKA_BOOTSTRAP_SERVERS not set")
	}

	// Channel to collect server errors (gRPC + HTTP + payment event consumer).
	serverErrors := make(chan error, 3)

	// Start gRPC server in background
	go func() {
		logger.Info("starting gRPC server", "address", grpcAddress)
		if err := grpcServer.Serve(grpcListener); err != nil {
			serverErrors <- fmt.Errorf("gRPC server error: %w", err)
		}
	}()

	// Start HTTP server in background
	go func() {
		logger.Info("starting HTTP server", "port", httpPort)
		if err := httpServer.Start(); err != nil {
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Start payment event consumer after servers are up.
	if paymentEventConsumer != nil {
		go func() {
			if err := paymentEventConsumer.Start(
				"financial-gateway.payment-captured.v1",
				"financial-gateway.payment-failed.v1",
			); err != nil {
				logger.Error("payment event consumer error", "error", err)
				serverErrors <- fmt.Errorf("payment event consumer error: %w", err)
			}
		}()
	}

	// Start billing workers in background (if enabled)
	workerCtx, workerCancel := context.WithCancel(ctx)
	defer workerCancel()
	var billingWg sync.WaitGroup
	if svcConfig.BillingEnabled && container.BillingCronScheduler != nil && container.DunningWorker != nil {
		billingWg.Add(2)
		go func() {
			defer billingWg.Done()
			if err := container.BillingCronScheduler.Start(workerCtx); err != nil {
				logger.Error("billing scheduler error", "error", err)
			}
		}()

		go func() {
			defer billingWg.Done()
			if err := container.DunningWorker.Start(workerCtx); err != nil {
				logger.Error("dunning worker error", "error", err)
			}
		}()

		logger.Info("billing workers started")
	}

	// Wait for interrupt signal or server error
	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	var runErr error
	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		logger.Error("server error", "error", err)
		runErr = err

		// Prefer graceful exit if a shutdown signal is already pending.
		// Without this, RunWithRetry would retry despite SIGTERM intent.
		select {
		case sig := <-sigChan:
			logger.Info("received signal during error handling, treating as shutdown", "signal", sig)
			runErr = nil
		default:
		}
	}

	// Graceful shutdown (runs for both signal and server error paths)
	logger.Info("shutting down servers...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaults.DefaultGracefulShutdown)
	defer cancel()

	// Stop billing workers and wait for goroutines to exit before database close.
	// Cancel the worker context first to unblock Start() select loops, then
	// call Stop() to signal internal shutdown channels and drain in-flight work.
	if svcConfig.BillingEnabled && container.BillingCronScheduler != nil && container.DunningWorker != nil {
		logger.Info("stopping billing workers...")
		workerCancel()
		container.BillingCronScheduler.Stop()
		container.DunningWorker.Stop()
		billingWg.Wait()
		logger.Info("billing workers stopped")
	}

	// Stop payment event consumer before closing connections
	if paymentEventConsumer != nil {
		paymentEventConsumer.Stop()
		logger.Info("payment event consumer stopped")
	}

	// Shutdown HTTP server (stop accepting new webhooks)
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shutdown HTTP server", "error", err)
	}

	// Gracefully stop gRPC server
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

	return runErr
}
