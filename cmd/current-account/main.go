// Package main is the entry point for the CurrentAccount service.
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

	pb "github.com/meridianhub/meridian/api/proto/meridian/current_account/v1"
	"github.com/meridianhub/meridian/internal/current-account/adapters/persistence"
	"github.com/meridianhub/meridian/internal/current-account/clients"
	"github.com/meridianhub/meridian/internal/current-account/service"
	"github.com/meridianhub/meridian/internal/platform/observability"
	"google.golang.org/grpc"
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

	logger.Info("starting current-account service",
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
		WithServiceName("current-account-service").
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
	repo := persistence.NewRepository(db)

	// Get external service targets from environment
	positionKeepingTarget := getEnvOrDefault("POSITION_KEEPING_TARGET", "positionkeeping-service:50051")
	financialAccountingTarget := getEnvOrDefault("FINANCIAL_ACCOUNTING_TARGET", "financialaccounting-service:50052")

	logger.Info("external service configuration",
		"position_keeping", positionKeepingTarget,
		"financial_accounting", financialAccountingTarget)

	// Create service with external clients and capture the clients for health checking
	currentAccountService, posKeepingClient, finAcctClient, err := createServiceWithClients(
		repo,
		positionKeepingTarget,
		financialAccountingTarget,
		logger,
		tracer,
	)
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}

	logger.Info("service initialized with external clients")

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
	pb.RegisterCurrentAccountServiceServer(grpcServer, currentAccountService)

	// Register health check service with dependency checking
	healthChecker := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:                repo,
		PositionKeepingClient:     posKeepingClient,
		FinancialAccountingClient: finAcctClient,
		Logger:                    logger,
		ServiceName:               "current-account",
		CheckTimeout:              5 * time.Second,
	})
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get port from environment
	port := getEnvOrDefault("GRPC_PORT", "50051")
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

	// Note: Custom health checker will automatically return NOT_SERVING when dependencies fail
	// No need to manually mark status as the checker evaluates actual dependency health

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
	dsn := getEnvOrDefault("DATABASE_URL", "postgres://meridian:meridian@localhost:5432/meridian?sslmode=disable")

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

// createServiceWithClients creates the service and returns it along with the external clients
// for use in health checking. This approach creates the clients once and shares them between
// the service and health checker to avoid duplicate connections.
func createServiceWithClients(
	repo *persistence.Repository,
	positionKeepingTarget string,
	financialAccountingTarget string,
	logger *slog.Logger,
	tracer *observability.Tracer,
) (*service.Service, clients.PositionKeepingClient, clients.FinancialAccountingClient, error) {
	// Create Position Keeping client
	posKeepingGRPCClient, err := clients.NewPositionKeepingClient(&clients.PositionKeepingClientConfig{
		Target:  positionKeepingTarget,
		Timeout: 30 * time.Second,
		Tracer:  tracer,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create position keeping client: %w", err)
	}

	// Wrap with resilience patterns (circuit breaker + retry)
	resilientPosKeepingClient := clients.NewResilientPositionKeepingClient(
		posKeepingGRPCClient,
		clients.ResilientClientConfig{
			Logger: logger,
		},
	)

	// Create Financial Accounting client
	finAcctGRPCClient, err := clients.NewFinancialAccountingClient(&clients.FinancialAccountingClientConfig{
		Target:  financialAccountingTarget,
		Timeout: 30 * time.Second,
		Tracer:  tracer,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create financial accounting client: %w", err)
	}

	// Wrap with resilience patterns (circuit breaker + retry)
	resilientFinAcctClient := clients.NewResilientFinancialAccountingClient(
		finAcctGRPCClient,
		clients.ResilientClientConfig{
			Logger: logger,
		},
	)

	// Create service with the pre-created clients
	svc, err := service.NewServiceWithExistingClients(
		repo,
		resilientPosKeepingClient,
		resilientFinAcctClient,
		logger,
		tracer,
	)
	if err != nil {
		return nil, nil, nil, err
	}

	// Return the service and the clients for use in health checking
	return svc, resilientPosKeepingClient, resilientFinAcctClient, nil
}
