// Package bootstrap provides shared infrastructure initialization utilities for Meridian services.
//
// This package consolidates the duplicated bootstrap patterns for database, Redis, gRPC,
// authentication, observability, and graceful shutdown that were previously scattered
// across individual services. It ensures consistent configuration, proper initialization
// order, and graceful shutdown across all Meridian microservices.
//
// # Quick Start
//
// A typical service main.go uses bootstrap like this:
//
//	func main() {
//	    ctx := context.Background()
//
//	    // 1. Initialize logger first
//	    logger := bootstrap.NewLogger("my-service", version, commit, buildDate)
//
//	    // 2. Initialize tracer for distributed tracing
//	    tracer, err := bootstrap.NewTracer(ctx, bootstrap.TracerConfig{
//	        ServiceName:    "my-service",
//	        ServiceVersion: version,
//	        Logger:         logger,
//	    })
//	    if err != nil {
//	        logger.Error("failed to initialize tracer", "error", err)
//	        os.Exit(1)
//	    }
//
//	    // 3. Connect to database
//	    dbConfig := bootstrap.DefaultDatabaseConfig()
//	    dbConfig.Logger = logger
//	    db, err := bootstrap.NewDatabase(ctx, dbConfig)
//	    if err != nil {
//	        logger.Error("failed to connect to database", "error", err)
//	        os.Exit(1)
//	    }
//
//	    // 4. Connect to Redis (optional)
//	    redisConfig := bootstrap.DefaultRedisConfig()
//	    redisConfig.Logger = logger
//	    redisClient, err := bootstrap.NewRedisClient(ctx, redisConfig)
//	    if err != nil {
//	        logger.Error("failed to connect to Redis", "error", err)
//	        os.Exit(1)
//	    }
//
//	    // 5. Initialize auth interceptor
//	    authCfg := bootstrap.DefaultAuthConfig(logger)
//	    authInterceptor, err := bootstrap.NewAuthInterceptor(ctx, authCfg)
//	    if err != nil {
//	        logger.Error("failed to initialize auth", "error", err)
//	        os.Exit(1)
//	    }
//
//	    // 6. Build gRPC server with interceptor chain
//	    grpcServer := bootstrap.NewGrpcServerBuilder(tracer, logger).
//	        WithAuthInterceptor(authInterceptor).
//	        Build()
//
//	    // Register your services here
//	    pb.RegisterMyServiceServer(grpcServer, myService)
//
//	    // 7. Set up shutdown orchestrator
//	    shutdown := bootstrap.NewShutdownOrchestrator(grpcServer, logger)
//	    shutdown.AddCleanup(func() error { return redisClient.Close() })
//	    shutdown.AddCleanup(func() error { bootstrap.CloseDatabase(db, logger); return nil })
//	    shutdown.AddCleanup(func() error { bootstrap.ShutdownTracer(tracer, logger); return nil })
//
//	    // 8. Start server and wait for shutdown
//	    errChan := make(chan error, 1)
//	    go func() {
//	        lis, _ := net.Listen("tcp", ":50051")
//	        errChan <- grpcServer.Serve(lis)
//	    }()
//
//	    if err := shutdown.Wait(errChan); err != nil {
//	        logger.Error("server error", "error", err)
//	        os.Exit(1)
//	    }
//	}
//
// # Components
//
// The package provides these main components:
//
// ## Database (PostgreSQL/CockroachDB)
//
//   - [DatabaseConfig] - Connection pool and timeout configuration
//   - [DefaultDatabaseConfig] - Loads config from environment variables
//   - [NewDatabase] - Creates GORM connection with health check
//   - [CloseDatabase] - Graceful connection cleanup
//
// ## Redis
//
//   - [RedisConfig] - Connection pool configuration
//   - [DefaultRedisConfig] - Loads config from environment variables
//   - [NewRedisClient] - Creates Redis client with health check
//
// ## Observability (OpenTelemetry)
//
//   - [TracerConfig] - Service name, version, and logger
//   - [NewTracer] - Creates tracer with OTLP exporter
//   - [ShutdownTracer] - Flushes pending spans and closes exporter
//
// ## Authentication (JWT/JWKS)
//
//   - [AuthConfig] - JWKS URL, timeouts, bypass methods
//   - [DefaultAuthConfig] - Loads config from environment variables
//   - [DefaultBypassMethods] - Standard health/reflection method list
//   - [NewAuthInterceptor] - Creates JWT validation interceptor
//
// ## gRPC Server
//
//   - [GrpcServerBuilder] - Fluent builder for server with interceptors
//   - [NewGrpcServerBuilder] - Creates builder with tracer and logger
//
// The builder ensures correct interceptor chain ordering:
//  1. Tracing (captures full request lifecycle)
//  2. Auth/TenantExtraction (validates JWT, populates context)
//  3. PlatformAdmin (if enabled, requires admin role)
//  4. Custom interceptors
//  5. Recovery (catches panics)
//
// ## Graceful Shutdown
//
//   - [ShutdownOrchestrator] - Coordinates OS signals and cleanup
//   - Executes cleanup functions in LIFO order
//   - Graceful gRPC shutdown with timeout fallback
//
// # Environment Variables
//
// The package reads configuration from these environment variables:
//
// ## Database
//
//   - DATABASE_URL: Connection string (default: postgres://meridian_user@localhost:26257/meridian?sslmode=disable)
//   - DB_MAX_OPEN_CONNS: Maximum open connections (default: 25)
//   - DB_MAX_IDLE_CONNS: Maximum idle connections (default: 5)
//   - DB_CONN_MAX_LIFETIME: Connection max lifetime (default: 5m)
//   - DB_CONN_MAX_IDLE_TIME: Connection max idle time (default: 10m)
//
// ## Redis
//
//   - REDIS_URL: Connection URL (default: redis://localhost:6379)
//   - REDIS_PASSWORD: Password override (default: empty)
//   - REDIS_DB: Database number 0-15 (default: 0)
//   - REDIS_POOL_SIZE: Pool size (default: 10)
//   - REDIS_MIN_IDLE_CONNS: Minimum idle connections (default: 2)
//
// ## OpenTelemetry
//
//   - OTEL_ENVIRONMENT: Deployment environment (default: development)
//   - OTEL_EXPORTER_OTLP_ENDPOINT: OTLP endpoint (default: alloy:4317)
//   - OTEL_TRACES_SAMPLER_ARG: Sampling rate 0.0-1.0 (default: 1.0)
//   - OTEL_TRACES_ENABLED: Enable tracing (default: true)
//   - OTEL_EXPORTER_OTLP_INSECURE: Disable TLS (default: true)
//
// ## Authentication
//
//   - AUTH_ENABLED: Enable authentication (default: true)
//   - AUTH_JWKS_URL: JWKS endpoint URL (required when enabled)
//   - AUTH_JWKS_REFRESH_TTL: Background refresh interval (default: 30m)
//   - AUTH_HTTP_TIMEOUT: HTTP timeout for JWKS requests (default: 30s)
//
// # Platform vs Tenant Services
//
// The gRPC builder supports two service types:
//
// Tenant-layer services (most services):
//
//	server := bootstrap.NewGrpcServerBuilder(tracer, logger).
//	    WithAuthInterceptor(authInterceptor).
//	    Build()
//
// Platform-layer services (e.g., tenant service):
//
//	server := bootstrap.NewGrpcServerBuilder(tracer, logger).
//	    WithAuthInterceptor(authInterceptor).
//	    WithPlatformAdmin().
//	    Build()
//
// Platform services require platform-admin role and do not require tenant_id claims.
//
// # Development Mode
//
// When AUTH_ENABLED=false, the gRPC builder uses TenantExtractionInterceptor
// which trusts the x-tenant-id header without JWT validation. This is suitable
// for local development but should never be used in production.
package bootstrap
