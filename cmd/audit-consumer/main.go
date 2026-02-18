// Package main is the entry point for the audit-consumer service.
//
// The audit-consumer processes audit events from a Kafka topic and writes them
// to tenant-scoped audit_log tables in a service's database. Each deployment is
// configured via environment variables to consume from a specific topic and write
// to a specific database, maintaining bounded context isolation per ADR-0002.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"

	"github.com/meridianhub/meridian/services/audit-worker/app"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// setupRoutes configures HTTP routes for the server.
func setupRoutes(mux *http.ServeMux, container *app.Container) {
	// Liveness probe - checks if the application is alive
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "alive")
	})

	// Readiness probe - checks if the application is ready to serve traffic
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), defaults.DefaultHealthCheckTimeout)
		defer cancel()
		healthy, dbErr, kafkaErr := container.HealthChecker.CheckAll(ctx)

		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, "not ready: db=%v, kafka=%v\n", dbErr, kafkaErr)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ready")
	})

	// Startup probe - checks if the application has started
	mux.HandleFunc("/health/startup", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), defaults.DefaultHealthCheckTimeout)
		defer cancel()
		healthy, dbErr, kafkaErr := container.HealthChecker.CheckAll(ctx)

		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = fmt.Fprintf(w, "not started: db=%v, kafka=%v\n", dbErr, kafkaErr)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "started")
	})

	// Root endpoint
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "audit-consumer v%s (commit: %s, built: %s)\n", Version, Commit, BuildDate)
	})

	// Prometheus metrics endpoint
	mux.Handle("/metrics", promhttp.Handler())
}

// createServer creates an HTTP server with proper security timeouts.
func createServer(port string) *http.Server {
	return &http.Server{
		Addr:              ":" + port,
		ReadHeaderTimeout: defaults.DefaultHTTPReadHeaderTimeout,
		ReadTimeout:       defaults.DefaultHTTPReadTimeout,
		WriteTimeout:      defaults.DefaultHTTPWriteTimeout,
		IdleTimeout:       2 * defaults.DefaultHTTPIdleTimeout, // Extended for consumer service
	}
}

func main() {
	// Setup logger early so we can use it throughout
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting audit-consumer service",
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
	// Load configuration from environment (permanent error if invalid)
	config, err := app.LoadConfig()
	if err != nil {
		return bootstrap.Permanent(fmt.Errorf("failed to load configuration: %w", err))
	}

	logger.Info("configuration loaded",
		"service_name", config.Service.Name,
		"kafka_topic", config.Kafka.Topic,
		"kafka_group_id", config.Kafka.GroupID,
		"port", config.Service.Port)

	// Initialize dependency container
	ctx := context.Background()
	container, err := app.NewContainer(ctx, config, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize container: %w", err)
	}

	// Start audit consumer
	if err := container.AuditConsumer.Start(config.Kafka.Topic); err != nil {
		return fmt.Errorf("failed to start audit consumer: %w", err)
	}
	logger.Info("audit consumer started", "topic", config.Kafka.Topic)

	// Setup HTTP routes with health checks
	mux := http.NewServeMux()
	setupRoutes(mux, container)

	// Create server with proper timeouts
	server := createServer(config.Service.Port)
	server.Handler = mux

	// Start server in background
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server", "port", config.Service.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Wait for interrupt signal or server error
	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	var runErr error
	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		logger.Error("HTTP server error", "error", err)
		runErr = err
	}

	// Graceful shutdown (runs for both signal and server error paths)
	logger.Info("shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.Service.GracefulShutdownTimeout)
	defer cancel()

	// Shutdown HTTP server
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}

	// Close container resources (includes consumer and database)
	if err := container.Close(shutdownCtx); err != nil {
		logger.Error("container close error", "error", err)
	}

	return runErr
}
