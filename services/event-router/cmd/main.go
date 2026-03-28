// Package main is the entry point for the event-router service.
// This service routes events from Kafka channels to registered handlers.
// The platform metering handler consumes audit events and transforms them into
// utilization measurements for Meridian's tenant-zero position-keeping billing.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	marketinformationv1 "github.com/meridianhub/meridian/api/proto/meridian/market_information/v1"
	auditdomain "github.com/meridianhub/meridian/services/audit-worker/domain"
	"github.com/meridianhub/meridian/services/event-router/adapters/grpc"
	"github.com/meridianhub/meridian/services/event-router/adapters/mds"
	"github.com/meridianhub/meridian/services/event-router/adapters/messaging"
	"github.com/meridianhub/meridian/services/event-router/app"
	"github.com/meridianhub/meridian/services/event-router/domain"
	platformgrpc "github.com/meridianhub/meridian/shared/pkg/grpc"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/kafka"
	"github.com/meridianhub/meridian/shared/platform/ports"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	grpclib "google.golang.org/grpc"
)

// Build information set via ldflags during compilation
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// readinessState tracks the readiness of service components for the /ready probe.
type readinessState struct {
	consumerInitialized bool
}

// createHTTPServer creates an HTTP server with health checks and metrics.
// Extracted from run() to enable unit testing without starting full service.
func createHTTPServer(httpPort string, readiness *readinessState, readinessMu *sync.RWMutex, logger *slog.Logger) *http.Server {
	httpMux := http.NewServeMux()

	// Health check endpoints
	httpMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("OK")); err != nil {
			logger.Warn("failed to write health response",
				"error", err,
				"endpoint", r.URL.Path,
				"remote_addr", r.RemoteAddr)
		}
	})

	httpMux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		readinessMu.RLock()
		defer readinessMu.RUnlock()
		if !readiness.consumerInitialized {
			w.WriteHeader(http.StatusServiceUnavailable)
			if _, err := w.Write([]byte("NOT_READY")); err != nil {
				logger.Warn("failed to write readiness response",
					"error", err,
					"endpoint", r.URL.Path,
					"remote_addr", r.RemoteAddr)
			}
			return
		}
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte("READY")); err != nil {
			logger.Warn("failed to write readiness response",
				"error", err,
				"endpoint", r.URL.Path,
				"remote_addr", r.RemoteAddr)
		}
	})

	// Prometheus metrics endpoint
	httpMux.Handle("/metrics", promhttp.Handler())

	return &http.Server{
		Addr:              fmt.Sprintf(":%s", httpPort),
		Handler:           httpMux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

func main() {
	// Initialize structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting event-router service",
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
	// Load configuration (permanent error if invalid)
	config, err := app.LoadConfig()
	if err != nil {
		return bootstrap.Permanent(fmt.Errorf("failed to load configuration: %w", err))
	}

	logger.Info("configuration loaded",
		"kafka_bootstrap_servers", config.KafkaBootstrapServers,
		"consumer_group_id", config.ConsumerGroupID,
		"audit_topics", config.AuditTopics,
		"position_keeping_endpoint", config.PositionKeepingEndpoint,
		"tenant_zero_id", config.TenantZeroID,
		"enable_mds_output", config.EnableMDSOutput,
		"mds_service_addr", config.MDSServiceAddr)

	// Create readiness tracker and HTTP server
	var (
		readiness   = &readinessState{}
		readinessMu = &sync.RWMutex{}
	)
	httpServer := createHTTPServer(config.HTTPPort, readiness, readinessMu, logger)
	serverErrors, httpCleanup := launchHTTPServer(httpServer, logger)
	defer httpCleanup()

	// Initialize upstream clients and Kafka consumer
	pkClient, consumer, mdPublisher, mdsConn, err := initConsumerPipeline(config, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := pkClient.Close(); err != nil {
			logger.Error("failed to close position keeping client", "error", err)
		}
	}()
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
		readinessMu.Lock()
		readiness.consumerInitialized = true
		readinessMu.Unlock()
		logger.Info("audit consumer ready")
	}()

	return awaitAndShutdown(httpServer, consumer, mdPublisher, mdsConn, serverErrors, consumerErrors, logger)
}

// initConsumerPipeline creates the Position Keeping client, optional MDS publisher,
// and Kafka audit consumer.
func initConsumerPipeline(config *app.Config, logger *slog.Logger) (*grpc.PositionKeepingGRPCClient, *messaging.AuditConsumer, *mds.MarketDataPublisher, *grpclib.ClientConn, error) {
	// Initialize Position Keeping client
	pkClient, err := initPKClient(config, logger)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	// Initialize MDS publisher (optional, controlled by feature flag)
	var consumerOpts []messaging.AuditConsumerOption
	var mdPublisher *mds.MarketDataPublisher
	var mdsConn *grpclib.ClientConn

	if config.EnableMDSOutput && config.MDSServiceAddr != "" {
		logger.Info("initializing MDS publisher",
			"mds_service_addr", config.MDSServiceAddr,
			"aggregation_window", config.MDSAggregationWindow,
			"flush_interval", config.MDSFlushInterval)

		mdPublisher, mdsConn, err = initMDSPublisher(config, logger)
		if err != nil {
			logger.Error("failed to initialize MDS publisher, continuing without MDS output",
				"error", err)
		} else {
			consumerOpts = append(consumerOpts, messaging.WithMDSPublisher(mdPublisher))
		}
	} else {
		logger.Info("MDS output disabled",
			"enable_mds_output", config.EnableMDSOutput,
			"mds_service_addr", config.MDSServiceAddr)
	}

	// Parse tenant mapping and create transformer
	transformer, err := createTransformer(config, logger)
	if err != nil {
		_ = pkClient.Close()
		return nil, nil, nil, nil, err
	}

	// Initialize Kafka consumer
	logger.Info("initializing kafka consumer",
		"topics", config.AuditTopics,
		"group_id", config.ConsumerGroupID)

	kafkaConfig := kafka.ConsumerConfig{
		BootstrapServers: config.KafkaBootstrapServers,
		GroupID:          config.ConsumerGroupID,
		ClientID:         "event-router",
		AutoOffsetReset:  "earliest",
		EnableAutoCommit: false,
	}

	consumer, err := messaging.NewAuditConsumer(kafkaConfig, transformer, pkClient, consumerOpts...)
	if err != nil {
		_ = pkClient.Close()
		return nil, nil, nil, nil, fmt.Errorf("failed to create audit consumer: %w", err)
	}

	return pkClient, consumer, mdPublisher, mdsConn, nil
}

// launchHTTPServer starts the HTTP server in a background goroutine and returns
// an error channel and a cleanup function that closes the server.
func launchHTTPServer(httpServer *http.Server, logger *slog.Logger) (chan error, func()) {
	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("starting HTTP server for health checks and metrics",
			"address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
			serverErrors <- fmt.Errorf("HTTP server error: %w", err)
		}
	}()
	cleanup := func() {
		if err := httpServer.Close(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Warn("failed to close HTTP server", "error", err)
		}
	}
	return serverErrors, cleanup
}

// createTransformer parses tenant-zero ID and account mapping, then creates the audit event transformer.
func createTransformer(config *app.Config, logger *slog.Logger) (*auditdomain.AuditEventTransformer, error) {
	tenantZeroID, err := uuid.Parse(config.TenantZeroID)
	if err != nil {
		return nil, bootstrap.Permanent(fmt.Errorf("invalid TENANT_ZERO_ID: %w", err))
	}

	tenantAccountMap, err := domain.ParseTenantAccountMapping(config.TenantAccountMapping)
	if err != nil {
		return nil, bootstrap.Permanent(fmt.Errorf("failed to load tenant account mapping: %w", err))
	}

	if _, exists := tenantAccountMap[tenantZeroID]; !exists {
		logger.Info("tenant-zero not found in TENANT_ACCOUNT_MAPPING, mapping to itself",
			"tenant_zero_id", tenantZeroID)
		tenantAccountMap[tenantZeroID] = tenantZeroID
	}
	logger.Info("tenant account mapping loaded", "mapping_count", len(tenantAccountMap))

	return auditdomain.NewAuditEventTransformer(tenantAccountMap), nil
}

// initPKClient creates the Position Keeping gRPC client.
func initPKClient(config *app.Config, logger *slog.Logger) (*grpc.PositionKeepingGRPCClient, error) {
	logger.Info("initializing position keeping client", "endpoint", config.PositionKeepingEndpoint)

	var pkPort int
	if lastColon := strings.LastIndex(config.PositionKeepingEndpoint, ":"); lastColon != -1 {
		if _, err := fmt.Sscanf(config.PositionKeepingEndpoint[lastColon:], ":%d", &pkPort); err != nil || pkPort == 0 {
			pkPort = ports.PositionKeeping
			logger.Warn("failed to parse port from POSITION_KEEPING_ENDPOINT, using default - verify endpoint format is 'host:port'",
				"endpoint", config.PositionKeepingEndpoint,
				"default_port", pkPort,
				"implication", "gRPC connection may fail if Position Keeping service uses a different port")
		}
	} else {
		pkPort = ports.PositionKeeping
		logger.Warn("no port found in POSITION_KEEPING_ENDPOINT, using default - verify endpoint includes port number",
			"endpoint", config.PositionKeepingEndpoint,
			"default_port", pkPort,
			"implication", "gRPC connection may fail if Position Keeping service uses a different port")
	}

	pkClient, err := grpc.NewPositionKeepingClient(&grpc.ClientConfig{
		ServiceName: "position-keeping",
		Namespace:   env.GetEnvOrDefault("K8S_NAMESPACE", "default"),
		Port:        pkPort,
		Timeout:     5 * time.Second,
		Logger:      logger,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create position keeping client: %w", err)
	}

	return pkClient, nil
}

// awaitAndShutdown waits for a shutdown signal or error, then gracefully stops all components.
func awaitAndShutdown(
	httpServer *http.Server,
	consumer *messaging.AuditConsumer,
	mdPublisher *mds.MarketDataPublisher,
	mdsConn *grpclib.ClientConn,
	serverErrors, consumerErrors chan error,
	logger *slog.Logger,
) error {
	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	var runErr error
	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		logger.Error("server error", "error", err)
		runErr = fmt.Errorf("server error: %w", err)
	case err := <-consumerErrors:
		logger.Error("consumer error", "error", err)
		runErr = fmt.Errorf("consumer error: %w", err)
	}

	logger.Info("shutting down server...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), defaults.DefaultRPCTimeout)
	defer cancel()

	logger.Info("stopping kafka consumer...")
	consumer.Stop()
	logger.Info("kafka consumer stopped")

	if mdPublisher != nil {
		logger.Info("flushing MDS publisher...")
		mdPublisher.Stop()
		logger.Info("MDS publisher stopped")
	}
	if mdsConn != nil {
		if err := mdsConn.Close(); err != nil {
			logger.Error("failed to close MDS gRPC connection", "error", err)
		}
	}

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	} else {
		logger.Info("HTTP server stopped")
	}

	return runErr
}

// initMDSPublisher creates and returns a MarketDataPublisher and its underlying gRPC connection.
// The caller is responsible for closing the connection after stopping the publisher.
func initMDSPublisher(config *app.Config, logger *slog.Logger) (*mds.MarketDataPublisher, *grpclib.ClientConn, error) {
	// Parse port from MDS service address
	var mdsPort int
	if lastColon := strings.LastIndex(config.MDSServiceAddr, ":"); lastColon != -1 {
		if _, err := fmt.Sscanf(config.MDSServiceAddr[lastColon:], ":%d", &mdsPort); err != nil || mdsPort == 0 {
			mdsPort = ports.MarketInformation
		}
	} else {
		mdsPort = ports.MarketInformation
	}

	// Extract service name from address (everything before the last colon)
	mdsServiceName := config.MDSServiceAddr
	if lastColon := strings.LastIndex(mdsServiceName, ":"); lastColon != -1 {
		mdsServiceName = mdsServiceName[:lastColon]
	}

	conn, err := platformgrpc.NewClient(context.Background(), platformgrpc.ClientConfig{
		ServiceName: mdsServiceName,
		Namespace:   env.GetEnvOrDefault("K8S_NAMESPACE", "default"),
		Port:        mdsPort,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create MDS gRPC connection: %w", err)
	}

	mdsClient := marketinformationv1.NewMarketInformationServiceClient(conn)

	publisher, err := mds.NewMarketDataPublisher(mdsClient, mds.Config{
		WindowSize:    config.MDSAggregationWindow,
		FlushInterval: config.MDSFlushInterval,
		Logger:        logger,
	})
	if err != nil {
		_ = conn.Close()
		return nil, nil, fmt.Errorf("failed to create MDS publisher: %w", err)
	}

	return publisher, conn, nil
}
