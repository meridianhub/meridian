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
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/meridianhub/meridian/internal/audit-consumer/app"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// setupRoutes configures HTTP routes for the server.
func setupRoutes(mux *http.ServeMux) {
	// Health check endpoints
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "alive")
	})

	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, "ready")
	})

	mux.HandleFunc("/health/startup", func(w http.ResponseWriter, _ *http.Request) {
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
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

func main() {
	log.Printf("audit-consumer v%s (commit: %s, built: %s)", Version, Commit, BuildDate)

	// Setup logger
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Load configuration from environment
	config, err := app.LoadConfig()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
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
		log.Fatalf("Failed to initialize container: %v", err)
	}

	// Start audit consumer
	if err := container.AuditConsumer.Start(config.Kafka.Topic); err != nil {
		log.Fatalf("Failed to start audit consumer: %v", err)
	}
	logger.Info("audit consumer started", "topic", config.Kafka.Topic)

	// Setup HTTP routes
	setupRoutes(http.DefaultServeMux)

	// Create server with proper timeouts
	server := createServer(config.Service.Port)

	// Start server in background
	go func() {
		logger.Info("starting HTTP server", "port", config.Service.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	logger.Info("shutdown signal received, starting graceful shutdown...")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), config.Service.GracefulShutdownTimeout)

	// Shutdown HTTP server
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}

	// Close container resources (includes consumer and database)
	closeErr := container.Close(shutdownCtx)

	// Cancel context after all shutdown operations complete
	cancel()

	if closeErr != nil {
		logger.Error("container close error", "error", closeErr)
		os.Exit(1)
	}

	logger.Info("shutdown complete")
}
