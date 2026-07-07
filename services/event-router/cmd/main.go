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

	// Initialize upstream clients and Kafka consumer. Readiness is driven by the
	// consumer's OnReady hook so /ready reflects genuine consumer state rather
	// than being flipped optimistically before the consumer is actually up.
	onReady := newReadinessCallback(readiness, readinessMu, logger)
	pipeline, err := initConsumerPipeline(config, onReady, logger)
	if err != nil {
		return err
	}
	defer pipeline.closeClients(logger)

	// Start consuming in background. Start blocks until shutdown; readiness is
	// signaled from inside the consume loop via onReady, not here.
	consumerErrors := make(chan error, 1)
	go func() {
		logger.Info("starting audit event consumption")
		if err := pipeline.consumer.Start(config.AuditTopics); err != nil {
			logger.Error("consumer error", "error", err)
			consumerErrors <- fmt.Errorf("consumer error: %w", err)
		}
	}()

	return awaitAndShutdown(httpServer, pipeline, serverErrors, consumerErrors, logger)
}

// consumerPipeline bundles the upstream clients and Kafka consumer that make up
// the event-router processing pipeline, along with the resources released on shutdown.
type consumerPipeline struct {
	pkClient    *grpc.PositionKeepingGRPCClient
	consumer    *messaging.AuditConsumer
	mdPublisher *mds.MarketDataPublisher
	mdsConn     *grpclib.ClientConn
	dlqProducer *kafka.DLQProducer
}

// closeClients releases the Kafka consumer and position-keeping client.
func (p *consumerPipeline) closeClients(logger *slog.Logger) {
	if err := p.consumer.Close(); err != nil {
		logger.Error("failed to close audit consumer", "error", err)
	}
	if err := p.pkClient.Close(); err != nil {
		logger.Error("failed to close position keeping client", "error", err)
	}
}

// newReadinessCallback returns the callback wired into the Kafka consumer's
// OnReady hook. It marks the consumer ready by writing a bool under the mutex,
// so it never blocks and can never stall the consume loop.
func newReadinessCallback(readiness *readinessState, mu *sync.RWMutex, logger *slog.Logger) func() {
	return func() {
		mu.Lock()
		readiness.consumerInitialized = true
		mu.Unlock()
		logger.Info("audit consumer ready")
	}
}

// initConsumerPipeline creates the Position Keeping client, optional MDS publisher,
// dead-letter-queue producer, and Kafka audit consumer. onReady is wired into the
// consumer so readiness reflects genuine consumer state.
func initConsumerPipeline(config *app.Config, onReady func(), logger *slog.Logger) (*consumerPipeline, error) {
	pkClient, err := initPKClient(config, logger)
	if err != nil {
		return nil, err
	}

	consumerOpts, mdPublisher, mdsConn := buildMDSOutput(config, logger)

	transformer, err := createTransformer(config, logger)
	if err != nil {
		_ = pkClient.Close()
		return nil, err
	}

	dlqProducer, dlqConfig, err := initDLQProducer(config, logger)
	if err != nil {
		_ = pkClient.Close()
		return nil, err
	}

	logger.Info("initializing kafka consumer",
		"topics", config.AuditTopics,
		"group_id", config.ConsumerGroupID)

	kafkaConfig := kafka.ConsumerConfig{
		BootstrapServers: config.KafkaBootstrapServers,
		GroupID:          config.ConsumerGroupID,
		ClientID:         "event-router",
		AutoOffsetReset:  "earliest",
		EnableAutoCommit: false,
		DLQProducer:      dlqProducer,
		DLQConfig:        &dlqConfig,
		OnReady:          onReady,
	}

	consumer, err := messaging.NewAuditConsumer(kafkaConfig, transformer, pkClient, consumerOpts...)
	if err != nil {
		_ = pkClient.Close()
		dlqProducer.Close()
		return nil, fmt.Errorf("failed to create audit consumer: %w", err)
	}

	return &consumerPipeline{
		pkClient:    pkClient,
		consumer:    consumer,
		mdPublisher: mdPublisher,
		mdsConn:     mdsConn,
		dlqProducer: dlqProducer,
	}, nil
}

// buildMDSOutput initializes the optional MDS publisher and returns the consumer
// options plus the resources released on shutdown. A failed MDS init is logged
// and the pipeline continues without MDS output (best-effort dual output).
func buildMDSOutput(config *app.Config, logger *slog.Logger) ([]messaging.AuditConsumerOption, *mds.MarketDataPublisher, *grpclib.ClientConn) {
	if !config.EnableMDSOutput || config.MDSServiceAddr == "" {
		logger.Info("MDS output disabled",
			"enable_mds_output", config.EnableMDSOutput,
			"mds_service_addr", config.MDSServiceAddr)
		return nil, nil, nil
	}

	logger.Info("initializing MDS publisher",
		"mds_service_addr", config.MDSServiceAddr,
		"aggregation_window", config.MDSAggregationWindow,
		"flush_interval", config.MDSFlushInterval)

	mdPublisher, mdsConn, err := initMDSPublisher(config, logger)
	if err != nil {
		logger.Error("failed to initialize MDS publisher, continuing without MDS output",
			"error", err)
		return nil, nil, nil
	}
	return []messaging.AuditConsumerOption{messaging.WithMDSPublisher(mdPublisher)}, mdPublisher, mdsConn
}

// initDLQProducer creates the Kafka dead-letter-queue producer for poison audit
// messages. After the configured retries are exhausted, a failing message is
// routed to a per-topic DLQ ("<topic>-dlq") instead of blocking the consumer or
// looping indefinitely, and the offset is committed so consumption proceeds.
func initDLQProducer(config *app.Config, logger *slog.Logger) (*kafka.DLQProducer, kafka.DLQConfig, error) {
	producer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: config.KafkaBootstrapServers,
		ClientID:         "event-router-dlq",
	})
	if err != nil {
		return nil, kafka.DLQConfig{}, fmt.Errorf("failed to create DLQ producer: %w", err)
	}

	dlqConfig := kafka.DefaultDLQConfig(config.ConsumerGroupID)
	dlqProducer, err := kafka.NewDLQProducer(producer, dlqConfig)
	if err != nil {
		producer.Close()
		return nil, kafka.DLQConfig{}, fmt.Errorf("failed to create DLQ producer wrapper: %w", err)
	}

	logger.Info("dead letter queue configured",
		"topic_suffix", dlqConfig.DLQTopicSuffix,
		"max_retries", dlqConfig.MaxRetries)
	return dlqProducer, dlqConfig, nil
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
	pipeline *consumerPipeline,
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
	pipeline.consumer.Stop()
	logger.Info("kafka consumer stopped")

	// Flush the consumer (no longer producing) before closing the DLQ producer
	// so any poison messages captured just before shutdown are delivered.
	if pipeline.dlqProducer != nil {
		if err := pipeline.dlqProducer.Flush(shutdownCtx); err != nil {
			logger.Error("failed to flush DLQ producer", "error", err)
		}
		pipeline.dlqProducer.Close()
		logger.Info("DLQ producer stopped")
	}

	if pipeline.mdPublisher != nil {
		logger.Info("flushing MDS publisher...")
		pipeline.mdPublisher.Stop()
		logger.Info("MDS publisher stopped")
	}
	if pipeline.mdsConn != nil {
		if err := pipeline.mdsConn.Close(); err != nil {
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
