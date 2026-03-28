// Package main is the entry point for the CurrentAccount service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/services/current-account/app"
	"github.com/meridianhub/meridian/services/current-account/service"
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

	logger.Info("starting current-account service",
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

	// Initialize dependency container
	container, err := app.NewContainer(ctx, logger, Version)
	if err != nil {
		runCancel()
		return err
	}
	defer func() {
		runCancel()
		container.Close()
	}()

	// Initialize Kafka producer lazily for outbox worker (optional - graceful degradation).
	// Channels communicate the outbox stop function and Kafka cleanup function back to the
	// main goroutine to avoid data races.
	outboxStopCh := make(chan func(), 1)
	kafkaCleanupCh := make(chan func(), 1)
	if container.BootstrapServers != "" {
		lazyKafka := bootstrap.NewLazyClient(ctx, "kafka-producer",
			func(_ context.Context) (*kafka.ProtoProducer, func(), error) {
				producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
					BootstrapServers: container.BootstrapServers,
					ClientID:         "current-account-outbox-worker",
					Acks:             "all",
					Retries:          5,
					Compression:      "snappy",
				})
				if err != nil {
					return nil, nil, err
				}
				return producer, func() { //nolint:contextcheck // FlushWithTimeout manages its own timeout
					logger.Info("flushing and closing Kafka producer...")
					if remaining := producer.FlushWithTimeout(5000); remaining > 0 {
						logger.Warn("some Kafka messages not delivered before close", "remaining", remaining)
					}
					producer.Close()
					logger.Info("Kafka producer closed")
				}, nil
			},
			bootstrap.WithLazyLogger(logger),
			bootstrap.WithLazyOnCleanup(func(cleanup func()) {
				kafkaCleanupCh <- cleanup
			}),
		)

		// Start outbox worker that will use the lazy Kafka producer once available.
		// Sends the stop function back via channel once the worker is running.
		go func() {
			for {
				producer, err := lazyKafka.Get()
				if err == nil {
					w := events.NewWorker(
						container.OutboxRepo,
						producer,
						events.DefaultWorkerConfig("current-account"),
						logger.With("component", "outbox_worker"),
					)
					w.Start(ctx)
					logger.Info("outbox worker started",
						"kafka_brokers", container.BootstrapServers)
					outboxStopCh <- w.Stop
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
				}
			}
		}()
	} else {
		logger.Warn("KAFKA_BOOTSTRAP_SERVERS not configured, outbox pattern disabled")
	}

	// Create gRPC server with interceptor chain
	// Order is handled by bootstrap: tracing -> auth -> recovery
	grpcServer, err := bootstrap.NewGrpcServerBuilder(container.Tracer, logger).
		WithAuthInterceptor(container.AuthInterceptor).
		Build()
	if err != nil {
		return fmt.Errorf("failed to build grpc server: %w", err)
	}

	// Register services
	pb.RegisterCurrentAccountServiceServer(grpcServer, container.Service)

	// Register health check service with database dependency checking.
	// Cross-service health checks (position-keeping, financial-accounting) are not
	// wired here - each service is independently deployable.
	healthChecker, err := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:   container.AccountRepo,
		Logger:       logger,
		ServiceName:  "current-account",
		CheckTimeout: defaults.DefaultHealthCheckTimeout,
	})
	if err != nil {
		return fmt.Errorf("failed to create health checker: %w", err)
	}
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get port from environment
	port := env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.CurrentAccount))
	address := fmt.Sprintf(":%s", port)

	// Create listener
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}

	// Start gRPC server in background
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting gRPC server", "address", address)
		if err := grpcServer.Serve(listener); err != nil {
			serverErrors <- err
		}
	}()

	// Wait for shutdown signal and orchestrate graceful shutdown
	orchestrator := bootstrap.NewShutdownOrchestrator(grpcServer, logger)

	// Register outbox worker and Kafka producer cleanup (LIFO: producer closes after worker stops).
	// The stop/cleanup functions arrive via channels once the background goroutine resolves Kafka.
	orchestrator.AddCleanup(func() error {
		select {
		case cleanupFn := <-kafkaCleanupCh:
			cleanupFn()
		default:
			// Kafka never resolved - nothing to close
		}
		return nil
	})
	orchestrator.AddCleanup(func() error {
		select {
		case stopFn := <-outboxStopCh:
			logger.Info("stopping outbox worker")
			stopFn()
			logger.Info("outbox worker stopped")
		default:
			// Worker never started (Kafka not resolved yet) - nothing to stop
		}
		return nil
	})

	// Cancel run-scoped context last (runs first in LIFO) to stop lazy resolution goroutines
	// before other cleanup proceeds. This prevents orphan goroutines across RunWithRetry retries.
	orchestrator.AddCleanup(func() error {
		runCancel()
		return nil
	})

	return orchestrator.Wait(serverErrors)
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
