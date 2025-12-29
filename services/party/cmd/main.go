// Package main is the entry point for the Party service.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/services/party/service"
	"github.com/meridianhub/meridian/shared/pkg/interceptors"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/observability"
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
	// Initialize structured logging with configurable log level
	logLevel := parseLogLevel(os.Getenv("LOG_LEVEL"))
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting party service",
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
		WithServiceName("party-service").
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

	// Create party service
	partyService, err := service.NewService(repo, logger)
	if err != nil {
		return fmt.Errorf("failed to create party service: %w", err)
	}

	logger.Info("party service initialized")

	// Initialize auth interceptor (optional - based on AUTH_ENABLED)
	authInterceptor, err := initAuth(ctx, logger)
	if err != nil {
		return fmt.Errorf("failed to initialize auth: %w", err)
	}

	// Create gRPC server with interceptor chain
	// Order: tracing → auth → recovery (recovery last to catch all panics)
	var unaryInterceptors []grpc.UnaryServerInterceptor
	var streamInterceptors []grpc.StreamServerInterceptor

	// 1. Tracing (always first for full request coverage)
	unaryInterceptors = append(unaryInterceptors, tracer.UnaryServerInterceptor())
	streamInterceptors = append(streamInterceptors, tracer.StreamServerInterceptor())

	// 2. Auth (JWT validation with JWKS - optional)
	if authInterceptor != nil {
		unaryInterceptors = append(unaryInterceptors, authInterceptor.UnaryInterceptor())
		streamInterceptors = append(streamInterceptors, authInterceptor.StreamInterceptor())
		logger.Info("auth interceptor enabled in chain")
	} else {
		// When auth is disabled, use TenantExtractionInterceptor to get tenant from header
		unaryInterceptors = append(unaryInterceptors, auth.TenantExtractionInterceptor())
		streamInterceptors = append(streamInterceptors, auth.TenantExtractionStreamInterceptor())
		logger.Info("tenant extraction interceptor enabled (auth disabled)")
	}

	// 3. Recovery (last in chain to catch all panics)
	unaryInterceptors = append(unaryInterceptors, interceptors.RecoveryUnaryInterceptor(logger))
	streamInterceptors = append(streamInterceptors, interceptors.RecoveryStreamInterceptor(logger))

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
	)

	// Register services
	pb.RegisterPartyServiceServer(grpcServer, partyService)

	// Register health check service
	healthChecker := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:   repo,
		Logger:       logger,
		ServiceName:  "party",
		CheckTimeout: 5 * time.Second,
	})
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get port from environment (default 50055 per task spec)
	port := env.GetEnvOrDefault("GRPC_PORT", "50055")
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
	dsn := env.GetEnvOrDefault("DATABASE_URL", "postgres://meridian_party_user@cockroachdb:26257/meridian_party?sslmode=disable")

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
	maxOpenConns := env.GetEnvAsInt("DB_MAX_OPEN_CONNS", 25)
	maxIdleConns := env.GetEnvAsInt("DB_MAX_IDLE_CONNS", 5)
	connMaxLifetime := env.GetEnvAsDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute)
	connMaxIdleTime := env.GetEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 10*time.Minute)

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

// initAuth initializes the JWT authentication interceptor if enabled.
// Returns nil if AUTH_ENABLED is false (default), allowing unauthenticated requests.
//
// Environment variables:
//   - AUTH_ENABLED: Set to "true" to enable JWT authentication (default: false)
//   - JWKS_URL: JWKS endpoint URL for JWT validation (required when enabled)
//   - JWKS_CACHE_TTL: How long to cache JWKS keys (default: 1h)
//   - JWKS_REFRESH_TTL: Background refresh interval for JWKS (default: 30m)
//   - JWKS_HTTP_TIMEOUT: HTTP client timeout for JWKS fetch (default: 10s)
//
// Note: The system is always multi-tenant. JWT tokens MUST include tenant_id claim.
// The JWKS provider starts a background refresh goroutine. This follows the
// existing pattern in other services (e.g., position-keeping) where the provider
// is not explicitly closed during shutdown, relying on process termination.
func initAuth(ctx context.Context, logger *slog.Logger) (*auth.Interceptor, error) {
	enabled := env.GetEnvAsBool("AUTH_ENABLED", false)
	if !enabled {
		logger.Info("auth disabled (set AUTH_ENABLED=true to enable)")
		return nil, nil //nolint:nilnil // Disabled mode intentionally returns no interceptor and no error
	}

	// Load JWKS configuration
	jwksURL := env.GetEnvOrDefault("JWKS_URL", "http://localhost:18080/realms/meridian/protocol/openid-connect/certs")
	cacheTTL := env.GetEnvAsDuration("JWKS_CACHE_TTL", 1*time.Hour)
	refreshTTL := env.GetEnvAsDuration("JWKS_REFRESH_TTL", 30*time.Minute)

	// Create JWKS provider with HTTP client
	httpTimeout := env.GetEnvAsDuration("JWKS_HTTP_TIMEOUT", 10*time.Second)
	httpClient := &http.Client{
		Timeout: httpTimeout,
	}
	jwksConfig := &auth.JWKSProviderConfig{
		URL:        jwksURL,
		Client:     httpClient,
		CacheTTL:   cacheTTL,
		RefreshTTL: refreshTTL,
	}

	provider, err := auth.NewJWKSProvider(ctx, jwksConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWKS provider: %w", err)
	}

	// Create JWT validator
	validator, err := auth.NewJWTValidatorWithJWKS(provider)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT validator: %w", err)
	}

	// Create interceptor with bypass methods for health checks and reflection
	interceptorConfig := &auth.InterceptorConfig{
		JWKSValidator: validator,
		BypassMethods: []string{
			"/grpc.health.v1.Health/Check",
			"/grpc.health.v1.Health/Watch",
			"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
			"/grpc.reflection.v1.ServerReflection/ServerReflectionInfo",
		},
	}

	interceptor, err := auth.NewAuthInterceptor(interceptorConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create auth interceptor: %w", err)
	}

	logger.Debug("auth interceptor initialized",
		"jwks_url", jwksURL,
		"cache_ttl", cacheTTL,
		"refresh_ttl", refreshTTL,
		"http_timeout", httpTimeout,
		"bypass_methods", len(interceptorConfig.BypassMethods))

	return interceptor, nil
}
