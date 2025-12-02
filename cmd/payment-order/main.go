// Package main is the entry point for the PaymentOrder service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	currentaccountv1 "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/payment_order/v1"
	"github.com/meridianhub/meridian/internal/payment-order/adapters/gateway"
	webhookhttp "github.com/meridianhub/meridian/internal/payment-order/adapters/http"
	"github.com/meridianhub/meridian/internal/payment-order/adapters/persistence"
	"github.com/meridianhub/meridian/internal/payment-order/service"
	"github.com/meridianhub/meridian/internal/platform/kafka"
	"github.com/meridianhub/meridian/internal/platform/observability"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Build information set via ldflags during compilation.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// ErrMissingHMACSecret is returned when the WEBHOOK_HMAC_SECRET environment variable is not set.
var ErrMissingHMACSecret = errors.New("WEBHOOK_HMAC_SECRET environment variable is required")

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
		"environment", tracerConfig.Environment)

	// Initialize database connection
	db, err := initDatabase(logger)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer closeDatabase(db, logger)
	logger.Info("database connection established")

	// Create repository
	repo := persistence.NewPaymentOrderRepository(db)

	// Get Kubernetes namespace from environment
	namespace := getEnvOrDefault("K8S_NAMESPACE", "default")

	// Create external clients
	currentAccountClient, cleanup, err := createCurrentAccountClient(namespace, logger)
	if err != nil {
		return fmt.Errorf("failed to create current account client: %w", err)
	}
	defer cleanup()

	// Create payment gateway
	paymentGateway := createPaymentGateway(logger)

	// Create Kafka producer
	kafkaProducer, err := createKafkaProducer(logger)
	if err != nil {
		return fmt.Errorf("failed to create Kafka producer: %w", err)
	}
	defer kafkaProducer.Close()

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

	// Create gRPC server
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(
			tracer.UnaryServerInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			tracer.StreamServerInterceptor(),
		),
	)

	// Register gRPC services
	pb.RegisterPaymentOrderServiceServer(grpcServer, paymentOrderService)
	grpc_health_v1.RegisterHealthServer(grpcServer, &simpleHealthServer{})
	reflection.Register(grpcServer)
	logger.Info("gRPC services registered")

	// Create HTTP webhook handler
	hmacSecret := []byte(getEnvOrDefault("WEBHOOK_HMAC_SECRET", ""))
	if len(hmacSecret) == 0 {
		return ErrMissingHMACSecret
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
	httpPort := getEnvAsInt("HTTP_PORT", 8080)
	httpServer, err := webhookhttp.NewServer(webhookhttp.ServerConfig{
		Port:               httpPort,
		WebhookHandler:     webhookHandler,
		Logger:             logger,
		RateLimitPerSecond: getEnvAsFloat("HTTP_RATE_LIMIT_PER_SECOND", 100),
		RateLimitBurst:     getEnvAsInt("HTTP_RATE_LIMIT_BURST", 200),
	})
	if err != nil {
		return fmt.Errorf("failed to create HTTP server: %w", err)
	}

	// Get gRPC port
	grpcPort := getEnvOrDefault("GRPC_PORT", "50054")
	grpcAddress := fmt.Sprintf(":%s", grpcPort)

	// Create gRPC listener
	grpcListener, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", grpcAddress)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", grpcAddress, err)
	}

	// Channel to collect server errors
	serverErrors := make(chan error, 2)

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

	// Wait for interrupt signal or server error
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		return err
	}

	// Graceful shutdown
	logger.Info("shutting down servers...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown HTTP server first (stop accepting new webhooks)
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

	return nil
}

// localPaymentOrderClient wraps the local service to implement the client interface.
type localPaymentOrderClient struct {
	service *service.Service
}

func (c *localPaymentOrderClient) UpdatePaymentOrder(ctx context.Context, req *pb.UpdatePaymentOrderRequest) (*pb.UpdatePaymentOrderResponse, error) {
	return c.service.UpdatePaymentOrder(ctx, req)
}

// simpleHealthServer implements grpc_health_v1.HealthServer with basic checks.
type simpleHealthServer struct {
	grpc_health_v1.UnimplementedHealthServer
}

func (s *simpleHealthServer) Check(_ context.Context, _ *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	}, nil
}

func (s *simpleHealthServer) Watch(_ *grpc_health_v1.HealthCheckRequest, server grpc_health_v1.Health_WatchServer) error {
	return server.Send(&grpc_health_v1.HealthCheckResponse{
		Status: grpc_health_v1.HealthCheckResponse_SERVING,
	})
}

// currentAccountGRPCClient implements service.CurrentAccountClient using gRPC.
type currentAccountGRPCClient struct {
	conn   *grpc.ClientConn
	client currentaccountv1.CurrentAccountServiceClient
}

func (c *currentAccountGRPCClient) InitiateLien(ctx context.Context, req *currentaccountv1.InitiateLienRequest) (*currentaccountv1.InitiateLienResponse, error) {
	return c.client.InitiateLien(ctx, req)
}

func (c *currentAccountGRPCClient) TerminateLien(ctx context.Context, req *currentaccountv1.TerminateLienRequest) (*currentaccountv1.TerminateLienResponse, error) {
	return c.client.TerminateLien(ctx, req)
}

func (c *currentAccountGRPCClient) ExecuteLien(ctx context.Context, req *currentaccountv1.ExecuteLienRequest) (*currentaccountv1.ExecuteLienResponse, error) {
	return c.client.ExecuteLien(ctx, req)
}

func (c *currentAccountGRPCClient) Close() error {
	return c.conn.Close()
}

// createCurrentAccountClient creates the CurrentAccount gRPC client.
func createCurrentAccountClient(namespace string, logger *slog.Logger) (service.CurrentAccountClient, func(), error) {
	target := fmt.Sprintf("dns:///current-account.%s.svc.cluster.local:50051", namespace)
	logger.Info("connecting to current-account service", "target", target)

	conn, err := grpc.NewClient(
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultServiceConfig(`{"loadBalancingConfig": [{"round_robin":{}}]}`),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to current-account service: %w", err)
	}

	client := &currentAccountGRPCClient{
		conn:   conn,
		client: currentaccountv1.NewCurrentAccountServiceClient(conn),
	}

	cleanup := func() {
		if err := client.Close(); err != nil {
			logger.Error("failed to close current-account client", "error", err)
		}
	}

	return client, cleanup, nil
}

// createPaymentGateway creates the payment gateway client.
func createPaymentGateway(logger *slog.Logger) gateway.PaymentGateway {
	gatewayURL := getEnvOrDefault("PAYMENT_GATEWAY_URL", "")
	if gatewayURL == "" {
		logger.Warn("PAYMENT_GATEWAY_URL not set, using mock gateway")
		return gateway.New(gateway.Config{UseMock: true})
	}

	return gateway.New(gateway.Config{
		Timeout:    30 * time.Second,
		MaxRetries: 3,
	})
}

// createKafkaProducer creates the Kafka producer.
func createKafkaProducer(logger *slog.Logger) (*kafka.ProtoProducer, error) {
	brokers := getEnvOrDefault("KAFKA_BROKERS", "kafka:9092")
	logger.Info("connecting to Kafka", "brokers", brokers)
	return kafka.NewProtoProducer(kafka.ProducerConfig{
		BootstrapServers: brokers,
		ClientID:         "payment-order-service",
	})
}

// initDatabase initializes the database connection with connection pooling.
func initDatabase(logger *slog.Logger) (*gorm.DB, error) {
	dsn := getEnvOrDefault("DATABASE_URL", "postgres://meridian:meridian@cockroachdb:26257/meridian?sslmode=disable")

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		SkipDefaultTransaction: true,
		PrepareStmt:            true,
		Logger:                 nil,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get database instance: %w", err)
	}

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
		"max_idle_conns", maxIdleConns)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return db, nil
}

// closeDatabase closes the database connection gracefully.
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

// Environment variable helpers

func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

func getEnvAsFloat(key string, defaultValue float64) float64 {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return defaultValue
	}
	return value
}

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
