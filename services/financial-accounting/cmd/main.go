// Package main is the entry point for the FinancialAccounting service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	financialaccountingv1 "github.com/meridianhub/meridian/api/proto/meridian/financial_accounting/v1"
	"github.com/meridianhub/meridian/services/financial-accounting/adapters/persistence"
	serviceobs "github.com/meridianhub/meridian/services/financial-accounting/observability"
	"github.com/meridianhub/meridian/services/financial-accounting/service"
	"github.com/meridianhub/meridian/shared/pkg/idempotency"
	"github.com/meridianhub/meridian/shared/pkg/interceptors"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

// Static errors for configuration validation
var (
	ErrBankCashAccountIDRequired = errors.New("BANK_CASH_ACCOUNT_ID environment variable is required")
	ErrBankCashAccountIDInvalid  = errors.New("BANK_CASH_ACCOUNT_ID must be a valid UUID")
)

func main() {
	// Initialize structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting financial-accounting service",
		"version", Version,
		"commit", Commit,
		"build_date", BuildDate)

	// Log environment for operational visibility
	environment := getEnvOrDefault("ENVIRONMENT", "production")
	logger.Info("service environment configured", "environment", environment)

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
		WithServiceName("financial-accounting-service").
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

	// Validate bank cash account ID is configured
	bankCashAccountID := getEnvOrDefault("BANK_CASH_ACCOUNT_ID", "")
	if bankCashAccountID == "" {
		return ErrBankCashAccountIDRequired
	}

	// Validate UUID format
	if len(bankCashAccountID) != 36 || bankCashAccountID[8] != '-' || bankCashAccountID[13] != '-' {
		return fmt.Errorf("%w: got %s", ErrBankCashAccountIDInvalid, bankCashAccountID)
	}

	logger.Info("bank cash account configured",
		"account_id", bankCashAccountID)

	// Create ledger repository
	ledgerRepo := persistence.NewLedgerRepository(db)

	// Create posting service (using validated bankCashAccountID from above)
	postingService := service.NewPostingService(ledgerRepo, bankCashAccountID)

	logger.Info("posting service initialized", "bank_cash_account_id", bankCashAccountID)

	// Create idempotency service (noop for now - Redis implementation for production)
	idempotencySvc := newNoopIdempotencyService()
	logger.Info("idempotency service initialized (noop mode)")

	// Create event publisher (noop for now - Kafka implementation for production)
	eventPublisher := &noopEventPublisher{}
	logger.Info("event publisher initialized (noop mode)")

	// Create Financial Accounting service
	financialAccountingSvc := service.NewFinancialAccountingService(ledgerRepo, eventPublisher, idempotencySvc)

	logger.Info("financial accounting service initialized")
	_ = postingService // Available for internal use

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
	}

	// 3. Recovery (last in chain to catch all panics)
	unaryInterceptors = append(unaryInterceptors, interceptors.RecoveryUnaryInterceptor(logger))
	streamInterceptors = append(streamInterceptors, interceptors.RecoveryStreamInterceptor(logger))

	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
	)

	// Register Financial Accounting gRPC service
	financialaccountingv1.RegisterFinancialAccountingServiceServer(grpcServer, financialAccountingSvc)

	// Register health check service with database connectivity check
	healthChecker := serviceobs.NewHealthChecker(serviceobs.HealthCheckerConfig{
		DB:           db,
		Logger:       logger,
		ServiceName:  "financial-accounting",
		CheckTimeout: 5 * time.Second,
	})
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get ports from environment
	port := getEnvOrDefault("GRPC_PORT", "50052")
	address := fmt.Sprintf(":%s", port)
	metricsPort := getEnvOrDefault("METRICS_PORT", "8082")

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

	// Start HTTP server for metrics and health endpoints
	httpMux := http.NewServeMux()
	httpMux.Handle("/metrics", promhttp.Handler())
	httpMux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		// Simple health endpoint for HTTP probes
		resp, err := healthChecker.Check(r.Context(), &grpc_health_v1.HealthCheckRequest{})
		if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("NOT_SERVING"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("SERVING"))
	})
	httpMux.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) {
		// Readiness endpoint - checks database connectivity
		resp, err := healthChecker.Check(r.Context(), &grpc_health_v1.HealthCheckRequest{Service: "database"})
		if err != nil || resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("NOT_READY"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("READY"))
	})

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%s", metricsPort),
		Handler:           httpMux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	go func() {
		logger.Info("starting HTTP server for metrics", "address", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server error", "error", err)
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
		return fmt.Errorf("server error: %w", err)
	}

	// Graceful shutdown
	logger.Info("shutting down server...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Shutdown HTTP server first (faster, allows metrics scraping during gRPC drain)
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	} else {
		logger.Info("HTTP server stopped")
	}

	// Gracefully stop gRPC server
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	// Wait for graceful stop or timeout
	select {
	case <-stopped:
		logger.Info("gRPC server stopped gracefully")
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

// noopIdempotencyService provides a no-operation implementation of idempotency.Service.
// This allows the service to start without Redis for development and testing.
// In production, use idempotency.NewRedisService for proper distributed idempotency.
type noopIdempotencyService struct{}

func newNoopIdempotencyService() *noopIdempotencyService {
	return &noopIdempotencyService{}
}

func (s *noopIdempotencyService) Check(_ context.Context, _ idempotency.Key) (*idempotency.Result, error) {
	return nil, idempotency.ErrResultNotFound
}

func (s *noopIdempotencyService) MarkPending(_ context.Context, _ idempotency.Key, _ time.Duration) error {
	return nil
}

func (s *noopIdempotencyService) StoreResult(_ context.Context, _ idempotency.Result) error {
	return nil
}

func (s *noopIdempotencyService) Delete(_ context.Context, _ idempotency.Key) error {
	return nil
}

func (s *noopIdempotencyService) Acquire(_ context.Context, _ idempotency.Key, _ idempotency.LockOptions) error {
	return nil
}

func (s *noopIdempotencyService) Release(_ context.Context, _ idempotency.Key, _ string) error {
	return nil
}

func (s *noopIdempotencyService) Refresh(_ context.Context, _ idempotency.Key, _ string, _ time.Duration) error {
	return nil
}

func (s *noopIdempotencyService) IsHeld(_ context.Context, _ idempotency.Key) (bool, error) {
	return false, nil
}

// noopEventPublisher provides a no-operation implementation of service.EventPublisher.
// This allows the service to start without Kafka for development and testing.
// In production, use messaging.NewKafkaEventPublisher for proper event publishing.
type noopEventPublisher struct{}

func (p *noopEventPublisher) Publish(_ context.Context, _ service.DomainEvent) error {
	return nil
}

func (p *noopEventPublisher) PublishBatch(_ context.Context, _ []service.DomainEvent) error {
	return nil
}

// getEnvAsBool returns the environment variable value as bool or default
func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := strings.TrimSpace(os.Getenv(key))
	if valueStr == "" {
		return defaultValue
	}

	switch strings.ToLower(valueStr) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return defaultValue
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
	enabled := getEnvAsBool("AUTH_ENABLED", false)
	if !enabled {
		logger.Info("auth disabled (set AUTH_ENABLED=true to enable)")
		return nil, nil //nolint:nilnil // Disabled mode intentionally returns no interceptor and no error
	}

	// Load JWKS configuration
	jwksURL := getEnvOrDefault("JWKS_URL", "http://localhost:18080/realms/meridian/protocol/openid-connect/certs")
	cacheTTL := getEnvAsDuration("JWKS_CACHE_TTL", 1*time.Hour)
	refreshTTL := getEnvAsDuration("JWKS_REFRESH_TTL", 30*time.Minute)

	// Create JWKS provider with HTTP client
	httpTimeout := getEnvAsDuration("JWKS_HTTP_TIMEOUT", 10*time.Second)
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
