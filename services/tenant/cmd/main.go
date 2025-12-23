// Package main is the entry point for the Tenant service.
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
	"syscall"
	"time"

	pb "github.com/meridianhub/meridian/api/proto/meridian/tenant/v1"
	"github.com/meridianhub/meridian/services/tenant/adapters/persistence"
	"github.com/meridianhub/meridian/services/tenant/clients"
	"github.com/meridianhub/meridian/services/tenant/provisioner"
	"github.com/meridianhub/meridian/services/tenant/service"
	"github.com/meridianhub/meridian/services/tenant/worker"
	sharedclients "github.com/meridianhub/meridian/shared/pkg/clients"
	"github.com/meridianhub/meridian/shared/pkg/interceptors"
	"github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/observability"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
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

// ErrMetricsServerStartupTimeout is returned when the metrics server fails to start within the timeout.
var ErrMetricsServerStartupTimeout = errors.New("metrics server startup timed out")

// envValueTrue is the string value for enabled environment variables.
const envValueTrue = "true"

// ErrJWKSURLRequired is returned when AUTH_ENABLED is true but AUTH_JWKS_URL is not set.
var ErrJWKSURLRequired = errors.New("AUTH_JWKS_URL is required when AUTH_ENABLED=true")

func main() {
	// Initialize structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting tenant service",
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
		WithServiceName("tenant-service").
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

	// Start metrics server with /metrics and /healthz endpoints
	metricsPort := getEnvOrDefault("METRICS_PORT", "9090")
	metricsAddr := fmt.Sprintf(":%s", metricsPort)

	// Create context for metrics server lifecycle
	metricsCtx, cancelMetrics := context.WithCancel(ctx)
	defer cancelMetrics()

	metricsServerErrors := make(chan error, 1)
	metricsReady := make(chan struct{})
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
		})

		server := &http.Server{
			Addr:              metricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       120 * time.Second,
		}

		// Graceful shutdown
		go func() {
			<-metricsCtx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil { //nolint:contextcheck // Intentionally using fresh context for shutdown grace period
				logger.Error("failed to shutdown metrics server", "error", err)
			}
		}()

		logger.Info("starting metrics server", "address", metricsAddr)

		// Create listener first to ensure port binding before signaling ready
		listener, err := (&net.ListenConfig{}).Listen(metricsCtx, "tcp", metricsAddr) //nolint:contextcheck // Using metricsCtx for lifecycle management
		if err != nil {
			metricsServerErrors <- fmt.Errorf("metrics server failed to bind: %w", err)
			return
		}

		// Signal that server is ready (port bound successfully)
		close(metricsReady)
		logger.Info("metrics server started", "address", metricsAddr)

		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			metricsServerErrors <- fmt.Errorf("metrics server failed: %w", err)
		}
	}()

	// Wait for metrics server to be ready or fail (with timeout)
	select {
	case <-metricsReady:
		// Server successfully bound to port
	case err := <-metricsServerErrors:
		return fmt.Errorf("metrics server startup failed: %w", err)
	case <-time.After(10 * time.Second):
		return ErrMetricsServerStartupTimeout
	}

	// Initialize database connection
	db, err := initDatabase(logger)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	defer closeDatabase(db, logger)

	logger.Info("database connection established")

	// Create repository
	repo := persistence.NewRepository(db)

	// Initialize schema provisioner (optional - skipped if SCHEMA_PROVISIONING_ENABLED is not "true")
	var schemaProvisioner provisioner.SchemaProvisioner
	provisioningEnabled := getEnvOrDefault("SCHEMA_PROVISIONING_ENABLED", "false")
	if provisioningEnabled == envValueTrue {
		config := provisioner.DefaultConfig()

		// Pass platform database connection (for tenant_provisioning table).
		// The provisioner will also connect to each service's database for schema creation.
		prov, err := provisioner.NewPostgresProvisioner(db, config)
		if err != nil {
			return fmt.Errorf("failed to create schema provisioner: %w", err)
		}
		schemaProvisioner = prov

		// Clean up service database connections on shutdown
		defer func() {
			if err := prov.Close(); err != nil {
				logger.Error("failed to close provisioner connections", "error", err)
			}
		}()

		logger.Info("schema provisioner initialized",
			"services", len(config.Services),
			"provisioning_timeout", config.ProvisioningTimeout)
	} else {
		logger.Warn("schema provisioning not enabled - tenant creation will not provision schemas",
			"hint", "set SCHEMA_PROVISIONING_ENABLED=true to enable schema provisioning")
	}

	// Initialize Party client (optional - skipped if PARTY_SERVICE_ENABLED is not "true")
	var partyClient clients.PartyClient
	namespace := getEnvOrDefault("K8S_NAMESPACE", "default")
	partyEnabled := getEnvOrDefault("PARTY_SERVICE_ENABLED", envValueTrue) == envValueTrue
	if partyEnabled {
		pc, err := clients.NewPartyClient(&sharedclients.PartyClientConfig{
			ServiceName: "party",
			Namespace:   namespace,
			Port:        50055,
			Timeout:     30 * time.Second,
			Tracer:      tracer,
		})
		if err != nil {
			return fmt.Errorf("failed to create party client: %w", err)
		}
		partyClient = pc
		defer func() {
			if err := pc.Close(); err != nil {
				logger.Error("failed to close party client", "error", err)
			}
		}()
		logger.Info("party client initialized",
			"service_name", "party",
			"namespace", namespace,
			"port", 50055)
	} else {
		logger.Warn("party client not configured - tenant creation will not register parties",
			"hint", "set PARTY_SERVICE_ENABLED=true to enable party registration")
	}

	// Initialize Redis client and slug cache (optional - skipped if REDIS_ENABLED is not "true")
	var slugCache *service.SlugCache
	redisEnabled := getEnvOrDefault("REDIS_ENABLED", envValueTrue) == envValueTrue
	if redisEnabled {
		redisClient, err := createRedisClient(logger)
		if err != nil {
			return fmt.Errorf("failed to create Redis client: %w", err)
		}
		defer func() {
			if err := redisClient.Close(); err != nil {
				logger.Error("failed to close Redis client", "error", err)
			}
		}()

		slugCache = service.NewSlugCache(redisClient)
		logger.Info("slug cache initialized with Redis backend")
	} else {
		logger.Warn("Redis not enabled - slug caching disabled",
			"hint", "set REDIS_ENABLED=true to enable slug caching")
	}

	// Create gRPC service
	tenantService := service.NewService(repo, schemaProvisioner, partyClient, slugCache, logger)

	// Create cached registry for validation middleware
	cachedRegistry := service.NewCachedRegistry(repo, service.CachedRegistryConfig{
		RefreshInterval: 60 * time.Second,
		Logger:          logger,
	})
	cachedRegistry.Start(ctx)

	logger.Info("cached tenant registry started",
		"refresh_interval", "60s")

	// Initialize provisioning worker (only if schema provisioning is enabled)
	var provisioningWorker *worker.ProvisioningWorker
	if provisioningEnabled == envValueTrue && schemaProvisioner != nil {
		pollInterval := getEnvAsDuration("PROVISIONING_POLL_INTERVAL", 30*time.Second)
		var err error
		provisioningWorker, err = worker.NewProvisioningWorker(
			repo,
			schemaProvisioner,
			pollInterval,
			logger,
		)
		if err != nil {
			return fmt.Errorf("failed to create provisioning worker: %w", err)
		}

		// Start worker in background goroutine
		go provisioningWorker.Start(ctx)

		logger.Info("provisioning worker started",
			"poll_interval", pollInterval)
	} else {
		logger.Info("provisioning worker disabled",
			"hint", "set SCHEMA_PROVISIONING_ENABLED=true to enable background provisioning")
	}

	// Initialize authentication (optional - disabled by default for development)
	// In production, set AUTH_JWKS_URL to enable platform-admin authentication.
	var authInterceptor *auth.Interceptor
	var jwksProvider *auth.JWKSProvider
	authEnabled := getEnvOrDefault("AUTH_ENABLED", "false") == envValueTrue
	if authEnabled {
		jwksURL := getEnvOrDefault("AUTH_JWKS_URL", "")
		if jwksURL == "" {
			return ErrJWKSURLRequired
		}

		// JWKS refresh TTL - configurable for key rotation scenarios
		jwksRefreshTTL := getEnvAsDuration("AUTH_JWKS_REFRESH_TTL", 5*time.Minute)

		// HTTP client with explicit timeout for JWKS fetches
		httpClient := &http.Client{
			Timeout: 10 * time.Second,
		}

		var err error
		jwksProvider, err = auth.NewJWKSProvider(ctx, &auth.JWKSProviderConfig{
			URL:        jwksURL,
			RefreshTTL: jwksRefreshTTL,
			Client:     httpClient,
		})
		if err != nil {
			return fmt.Errorf("failed to create JWKS provider: %w", err)
		}
		defer func() {
			if err := jwksProvider.Close(); err != nil {
				logger.Error("failed to close JWKS provider", "error", err)
			}
		}()

		jwksValidator, err := auth.NewJWTValidatorWithJWKS(jwksProvider)
		if err != nil {
			return fmt.Errorf("failed to create JWT validator: %w", err)
		}

		authInterceptor, err = auth.NewAuthInterceptor(&auth.InterceptorConfig{
			JWKSValidator: jwksValidator,
			BypassMethods: []string{
				"/grpc.health.v1.Health/Check",
				"/grpc.health.v1.Health/Watch",
				"/grpc.reflection.v1alpha.ServerReflection/ServerReflectionInfo",
			},
		})
		if err != nil {
			return fmt.Errorf("failed to create auth interceptor: %w", err)
		}

		logger.Info("platform authentication enabled",
			"jwks_url", jwksURL,
			"jwks_refresh_ttl", jwksRefreshTTL,
			"required_roles", []string{auth.RolePlatformAdmin, auth.RoleSuperAdmin})
	} else {
		logger.Warn("platform authentication disabled",
			"hint", "set AUTH_ENABLED=true and AUTH_JWKS_URL to enable authentication")
	}

	// Build interceptor chains based on auth configuration.
	//
	// Interceptor chain order (executed in sequence):
	//   1. Tracing      - Creates OpenTelemetry span for the request
	//   2. PlatformAuth - Validates JWT and populates claims in context (no tenant requirement)
	//   3. PlatformAdmin - Requires platform-admin/super-admin role, rejects tenant-scoped tokens
	//   4. Recovery     - Catches panics and converts them to gRPC errors
	//
	// Order matters because:
	//   - Tracing must be first to capture the full request lifecycle including auth failures
	//   - PlatformAuth must precede PlatformAdmin to populate claims that PlatformAdmin validates
	//   - Recovery must be last to catch panics from any preceding interceptor or handler
	var unaryInterceptors []grpc.UnaryServerInterceptor
	var streamInterceptors []grpc.StreamServerInterceptor

	// 1. Tracing - captures full request lifecycle
	unaryInterceptors = append(unaryInterceptors, tracer.UnaryServerInterceptor())
	streamInterceptors = append(streamInterceptors, tracer.StreamServerInterceptor())

	// 2-3. Auth interceptors (if enabled)
	if authInterceptor != nil {
		// 2. PlatformAuth - validates JWT without requiring tenant claims, populates claims in context
		unaryInterceptors = append(unaryInterceptors, authInterceptor.PlatformUnaryInterceptor())
		streamInterceptors = append(streamInterceptors, authInterceptor.PlatformStreamInterceptor())
		// 3. PlatformAdmin - requires platform-admin/super-admin role, rejects tenant-scoped tokens
		unaryInterceptors = append(unaryInterceptors, auth.PlatformAdminInterceptor())
		streamInterceptors = append(streamInterceptors, auth.PlatformAdminStreamInterceptor())
	}

	// 4. Recovery - catches panics from any preceding interceptor or handler
	unaryInterceptors = append(unaryInterceptors, interceptors.RecoveryUnaryInterceptor(logger))
	streamInterceptors = append(streamInterceptors, interceptors.RecoveryStreamInterceptor(logger))

	// Create gRPC server with interceptor chain
	grpcServer := grpc.NewServer(
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
	)

	// Register services
	pb.RegisterTenantServiceServer(grpcServer, tenantService)

	// Register health check service
	healthChecker := service.NewHealthChecker(service.HealthCheckerConfig{
		Repository:  repo,
		Logger:      logger,
		ServiceName: "tenant",
		Timeout:     5 * time.Second,
	})
	grpc_health_v1.RegisterHealthServer(grpcServer, healthChecker)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

	logger.Info("gRPC services registered")

	// Get port from environment
	port := getEnvOrDefault("GRPC_PORT", "50056")
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
		return fmt.Errorf("gRPC server error: %w", err)
	case err := <-metricsServerErrors:
		return fmt.Errorf("metrics server error: %w", err)
	}

	// Graceful shutdown
	logger.Info("shutting down server...")

	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Stop provisioning worker first if it's running
	if provisioningWorker != nil {
		logger.Info("stopping provisioning worker...")
		provisioningWorker.Stop()
		logger.Info("provisioning worker stopped")
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
		logger.Info("server stopped gracefully")
	case <-shutdownCtx.Done():
		logger.Warn("graceful shutdown timeout, forcing stop")
		grpcServer.Stop()
	}

	return nil
}

// initDatabase initializes the database connection with connection pooling.
func initDatabase(logger *slog.Logger) (*gorm.DB, error) {
	dsn := getEnvOrDefault("DATABASE_URL", "postgres://meridian_platform_user@cockroachdb:26257/meridian_platform?sslmode=disable")

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

// getEnvOrDefault returns the environment variable value or default.
func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// getEnvAsInt returns the environment variable value as int or default.
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

// getEnvAsDuration returns the environment variable value as duration or default.
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

// createRedisClient creates and initializes a Redis client from environment configuration.
func createRedisClient(logger *slog.Logger) (*redis.Client, error) {
	redisURL := getEnvOrDefault("REDIS_URL", "redis://localhost:6379")
	redisPassword := getEnvOrDefault("REDIS_PASSWORD", "")
	redisDB := getEnvAsInt("REDIS_DB", 0)
	poolSize := getEnvAsInt("REDIS_POOL_SIZE", 10)
	minIdleConns := getEnvAsInt("REDIS_MIN_IDLE_CONNS", 2)

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid REDIS_URL: %w", err)
	}

	// Override with explicit config if set
	if redisPassword != "" {
		opt.Password = redisPassword
	}
	opt.DB = redisDB
	opt.PoolSize = poolSize
	opt.MinIdleConns = minIdleConns

	client := redis.NewClient(opt)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	logger.Info("Redis client connected",
		"url", redisURL,
		"db", redisDB,
		"pool_size", poolSize,
		"min_idle_conns", minIdleConns)

	return client, nil
}
