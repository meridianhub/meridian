// Package main is the entry point for the Payment Order service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/internal/payment-order/adapters/gateway"
	"github.com/meridianhub/meridian/internal/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/internal/payment-order/service"
	"github.com/meridianhub/meridian/internal/platform/kafka"
	"github.com/meridianhub/meridian/internal/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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

	logger.Info("starting payment-order service",
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
	ctx := context.Background()

	// Initialize OpenTelemetry tracer
	tracerConfig, err := observability.DefaultConfig()
	if err != nil {
		return fmt.Errorf("failed to load tracer config: %w", err)
	}

	// Override service name and version from build info
	tracerConfig = tracerConfig.
		WithServiceName("payment-order-service").
		WithServiceVersion(Version)

	tracer, err := observability.NewTracer(ctx, tracerConfig)
	if err != nil {
		return fmt.Errorf("failed to initialize tracer: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tracer.Shutdown(shutdownCtx); err != nil {
			logger.Error("failed to shutdown tracer", "error", err)
		}
	}()

	logger.Info("tracer initialized",
		"service_name", tracerConfig.ServiceName,
		"environment", tracerConfig.Environment,
		"otlp_endpoint", tracerConfig.OTLPEndpoint,
		"sampling_rate", tracerConfig.SamplingRate)

	// Initialize database connection
	db, err := initDatabase(logger)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer closeDatabase(db, logger)

	logger.Info("database connection established")

	// Create repository
	repo := persistence.NewPaymentOrderRepository(db)

	// Get Kubernetes namespace from environment (defaults to "default")
	namespace := getEnvOrDefault("K8S_NAMESPACE", "default")

	// Create Current Account client for lien operations
	currentAccountClient, err := createCurrentAccountClient(namespace, logger, tracer)
	if err != nil {
		return fmt.Errorf("failed to create current account client: %w", err)
	}
	defer func() {
		if err := currentAccountClient.Close(); err != nil {
			logger.Error("failed to close current account client", "error", err)
		}
	}()

	logger.Info("current account client initialized",
		"service", "current-account."+namespace+".svc.cluster.local:50051")

	// Create Kafka producer for event publishing
	kafkaBrokers := getEnvOrDefault("KAFKA_BROKERS", "kafka:9092")
	kafkaProducer, err := kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: kafkaBrokers,
		ClientID:         "payment-order-service",
	})
	if err != nil {
		return fmt.Errorf("failed to create kafka producer: %w", err)
	}
	defer kafkaProducer.Close()

	logger.Info("kafka producer initialized", "brokers", kafkaBrokers)

	// Create payment gateway (mock for now)
	paymentGateway := gateway.NewMockGateway(gateway.MockGatewayConfig{
		Latency:     100 * time.Millisecond,
		FailureRate: 0.0, // No simulated failures in production
	})

	logger.Info("payment gateway initialized (mock)")

	// Create payment order service
	paymentOrderService, err := service.NewServiceWithConfig(service.Config{
		Repository:           repo,
		CurrentAccountClient: currentAccountClient,
		PaymentGateway:       paymentGateway,
		KafkaProducer:        kafkaProducer,
		Logger:               logger,
		Tracer:               tracer,
	})
	if err != nil {
		return fmt.Errorf("failed to create payment order service: %w", err)
	}

	logger.Info("payment order service initialized")

	// Create gRPC server with observability interceptors
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			tracer.UnaryServerInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			tracer.StreamServerInterceptor(),
		),
	)

	// Register services
	pb.RegisterPaymentOrderServiceServer(grpcServer, paymentOrderService)

	// Register health check service
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	healthServer.SetServingStatus("meridian.payment_order.v1.PaymentOrderService", grpc_health_v1.HealthCheckResponse_SERVING)
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get port from environment
	port := getEnvOrDefault("GRPC_PORT", "50054")
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

	// Wait for interrupt signal or server error
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		return fmt.Errorf("server error: %w", err)
	}

	// Graceful shutdown
	logger.Info("shutting down server...")

	// Mark health as not serving
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Gracefully stop gRPC server
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	// Wait for graceful stop or timeout
	select {
	case <-stopped:
		logger.Info("server stopped gracefully")
	case <-shutdownCtx.Done():
		logger.Warn("graceful shutdown timeout, forcing stop")
		grpcServer.Stop()
	}

	return nil
}

// initDatabase initializes the database connection with connection pooling
func initDatabase(logger *slog.Logger) (*gorm.DB, error) {
	dsn := getEnvOrDefault("DATABASE_URL", "postgres://meridian:meridian@cockroachdb:26257/meridian?sslmode=disable")

	// Open database connection
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		// Disable default transaction for better performance
		SkipDefaultTransaction: true,
		// Prepare statements for better performance
		PrepareStmt: true,
		Logger:      nil, // Use slog instead of gorm's default logger
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	// Configure connection pool
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database instance: %w", err)
	}

	// Connection pool settings
	maxOpenConns := getEnvAsInt("DB_MAX_OPEN_CONNS", 25)
	maxIdleConns := getEnvAsInt("DB_MAX_IDLE_CONNS", 5)
	connMaxLifetime := getEnvAsDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute)
	connMaxIdleTime := getEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 10*time.Minute)

	sqlDB.SetMaxOpenConns(maxOpenConns)
	sqlDB.SetMaxIdleConns(maxIdleConns)
	sqlDB.SetConnMaxLifetime(connMaxLifetime)
	sqlDB.SetConnMaxIdleTime(connMaxIdleTime)

	logger.Info("database connection pool configured",
		"max_open_conns", maxOpenConns,
		"max_idle_conns", maxIdleConns,
		"conn_max_lifetime", connMaxLifetime,
		"conn_max_idle_time", connMaxIdleTime)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// closeDatabase closes the database connection gracefully
func closeDatabase(db *gorm.DB, logger *slog.Logger) {
	sqlDB, err := db.DB()
	if err != nil {
		logger.Error("failed to get database instance for closing", "error", err)
		return
	}

	if err := sqlDB.Close(); err != nil {
		logger.Error("failed to close database connection", "error", err)
	} else {
		logger.Info("database connection closed")
	}
}

// currentAccountClient implements service.CurrentAccountClient using gRPC
type currentAccountClient struct {
	conn   *grpc.ClientConn
	client currentaccountv1.CurrentAccountServiceClient
}

// createCurrentAccountClient creates a gRPC client for the CurrentAccount service
func createCurrentAccountClient(namespace string, _ *slog.Logger, tracer *observability.Tracer) (service.CurrentAccountClient, error) {
	// Build the service address using DNS-based service discovery
	address := fmt.Sprintf("dns:///current-account.%s.svc.cluster.local:50051", namespace)

	// Create gRPC connection with tracing interceptors
	conn, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingPolicy":"round_robin"}`),
		grpc.WithUnaryInterceptor(tracer.UnaryClientInterceptor()),
		grpc.WithStreamInterceptor(tracer.StreamClientInterceptor()),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to current-account service: %w", err)
	}

	return &currentAccountClient{
		conn:   conn,
		client: currentaccountv1.NewCurrentAccountServiceClient(conn),
	}, nil
}

// InitiateLien creates a fund reservation on an account
func (c *currentAccountClient) InitiateLien(ctx context.Context, req *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
	return c.client.InitiateLien(ctx, req)
}

// TerminateLien releases a reservation without executing
func (c *currentAccountClient) TerminateLien(ctx context.Context, req *currentaccountv1.TerminateLienRequest) (*currentaccountv1.TerminateLienResponse, error) {
	return c.client.TerminateLien(ctx, req)
}

// ExecuteLien converts a reservation to an actual debit
func (c *currentAccountClient) ExecuteLien(ctx context.Context, req *currentaccountv1.ExecuteLienRequest) (*currentaccountv1.ExecuteLienResponse, error) {
	return c.client.ExecuteLien(ctx, req)
}

// Close terminates the client connection
func (c *currentAccountClient) Close() error {
	return c.conn.Close()
}

// getEnvOrDefault returns the environment variable value or default
func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// getEnvAsInt returns the environment variable value as int or default
func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	var value int
	if _, err := fmt.Sscanf(valueStr, "%d", &value); err != nil {
		return defaultValue
	}
	return value
}

// getEnvAsDuration returns the environment variable value as duration or default
func getEnvAsDuration(key string, defaultValue time.Duration) time.Duration {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := time.ParseDuration(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}
