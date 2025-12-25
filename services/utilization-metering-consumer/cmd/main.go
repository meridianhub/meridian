// Package main is the entry point for the utilization-metering-consumer service.
// This service consumes audit events from all services and transforms them into
// utilization measurements for Meridian's tenant-zero position-keeping billing.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	auditdomain "github.com/meridianhub/meridian/internal/audit-consumer/domain"
	"github.com/meridianhub/meridian/services/utilization-metering-consumer/adapters/grpc"
	"github.com/meridianhub/meridian/services/utilization-metering-consumer/adapters/messaging"
	"github.com/meridianhub/meridian/services/utilization-metering-consumer/app"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Build information set via ldflags during compilation
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

func main() {
	// Initialize structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting utilization-metering-consumer service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	// Run the service
	if err := run(logger); err != nil {
		logger.Error("service failed", "error", err)
		os.Exit(1)
	}

	logger.Info("service stopped gracefully")
}

func run(logger *slog.Logger) error {
	// Load configuration
	config, err := app.LoadConfig()
	if err != nil {
		return fmt.Errorf("failed to load configuration: %w", err)
	}

	logger.Info("configuration loaded",
		"kafka_bootstrap_servers", config.KafkaBootstrapServers,
		"consumer_group_id", config.ConsumerGroupID,
		"audit_topics", config.AuditTopics,
		"position_keeping_endpoint", config.PositionKeepingEndpoint,
		"tenant_zero_id", config.TenantZeroID)

	// Create readiness tracker
	type readinessState struct {
		consumerInitialized bool
	}
	var (
		readiness   = &readinessState{}
		readinessMu = &sync.RWMutex{}
	)

	// Create HTTP server for health checks and metrics
	httpMux := http.NewServeMux()

	// Health check endpoints
	httpMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	httpMux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		readinessMu.RLock()
		defer readinessMu.RUnlock()
		if !readiness.consumerInitialized {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("NOT_READY"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("READY"))
	})

	// Prometheus metrics endpoint
	httpMux.Handle("/metrics", promhttp.Handler())

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%s", config.HTTPPort),
		Handler:           httpMux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Start HTTP server in background
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server for health checks and metrics",
			"address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()

	// Initialize Position Keeping client
	logger.Info("initializing position keeping client", "endpoint", config.PositionKeepingEndpoint)

	// Parse port from endpoint (format: "host:port")
	// Handle Kubernetes DNS names like "position-keeping.default.svc.cluster.local:50053"
	var pkPort int
	if lastColon := strings.LastIndex(config.PositionKeepingEndpoint, ":"); lastColon != -1 {
		if _, err := fmt.Sscanf(config.PositionKeepingEndpoint[lastColon:], ":%d", &pkPort); err != nil || pkPort == 0 {
			// Default to 50053 if parsing fails
			pkPort = 50053
			logger.Warn("failed to parse port from POSITION_KEEPING_ENDPOINT, using default",
				"endpoint", config.PositionKeepingEndpoint,
				"default_port", pkPort)
		}
	} else {
		// No colon found, use default port
		pkPort = 50053
		logger.Warn("no port found in POSITION_KEEPING_ENDPOINT, using default",
			"endpoint", config.PositionKeepingEndpoint,
			"default_port", pkPort)
	}

	pkClient, err := grpc.NewPositionKeepingClient(&grpc.ClientConfig{
		ServiceName:    "position-keeping",
		Namespace:      getEnvOrDefault("K8S_NAMESPACE", "default"),
		Port:           pkPort,
		Timeout:        10 * time.Second,
		Logger:         logger,
		SimulationMode: true, // TODO: Set to false when RecordMeasurement endpoint exists
	})
	if err != nil {
		return fmt.Errorf("failed to create position keeping client: %w", err)
	}
	defer func() {
		if err := pkClient.Close(); err != nil {
			logger.Error("failed to close position keeping client", "error", err)
		}
	}()

	// Parse tenant zero ID
	tenantZeroID, err := uuid.Parse(config.TenantZeroID)
	if err != nil {
		return fmt.Errorf("invalid TENANT_ZERO_ID: %w", err)
	}

	// For now, we map all tenants to tenant-zero's billing account
	// In a real implementation, this would be loaded from configuration or a database
	// TODO: Load tenant-to-account mapping from configuration or database
	tenantAccountMap := make(map[uuid.UUID]uuid.UUID)
	// Map tenant-zero to itself for self-billing
	tenantAccountMap[tenantZeroID] = tenantZeroID

	// Initialize transformer with tenant account mapping
	transformer := auditdomain.NewAuditEventTransformer(tenantAccountMap)

	// Initialize Kafka consumer
	logger.Info("initializing kafka consumer",
		"topics", config.AuditTopics,
		"group_id", config.ConsumerGroupID)

	kafkaConfig := kafka.ConsumerConfig{
		BootstrapServers: config.KafkaBootstrapServers,
		GroupID:          config.ConsumerGroupID,
		ClientID:         "utilization-metering-consumer",
		AutoOffsetReset:  "earliest",
		EnableAutoCommit: false, // Manual commit for at-least-once semantics
	}

	consumer, err := messaging.NewAuditConsumer(kafkaConfig, transformer, pkClient)
	if err != nil {
		return fmt.Errorf("failed to create audit consumer: %w", err)
	}
	defer func() {
		if err := consumer.Close(); err != nil {
			logger.Error("failed to close audit consumer", "error", err)
		}
	}()

	// Start consuming in background
	consumerErrors := make(chan error, 1)
	go func() {
		logger.Info("starting audit event consumption")
		if err := consumer.Start(config.AuditTopics); err != nil {
			logger.Error("consumer error", "error", err)
			consumerErrors <- fmt.Errorf("consumer error: %w", err)
			return
		}

		// Mark consumer as initialized for readiness probe after successful start
		readinessMu.Lock()
		readiness.consumerInitialized = true
		readinessMu.Unlock()
		logger.Info("audit consumer ready")
	}()

	// Wait for interrupt signal, server error, or consumer error
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	case err := <-consumerErrors:
		return fmt.Errorf("consumer error: %w", err)
	}

	// Graceful shutdown
	logger.Info("shutting down server...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop Kafka consumer first to drain in-flight messages
	logger.Info("stopping kafka consumer...")
	consumer.Stop()
	logger.Info("kafka consumer stopped")

	// Shutdown HTTP server
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	} else {
		logger.Info("HTTP server stopped")
	}

	return nil
}

// getEnvOrDefault returns the value of an environment variable or a default value.
func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
