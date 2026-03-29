// Package main is the entry point for the Gateway service.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"net/url"
	"strings"
	"time"

	gateway "github.com/meridianhub/meridian/services/api-gateway"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/meridianhub/meridian/services/api-gateway/eventstream/adapters"
	gwhealth "github.com/meridianhub/meridian/services/api-gateway/health"
	identitypersistence "github.com/meridianhub/meridian/services/identity/adapters/persistence"
	identityconnector "github.com/meridianhub/meridian/services/identity/connector"
	"github.com/meridianhub/meridian/shared/pkg/health"
	platformauth "github.com/meridianhub/meridian/shared/platform/auth"
	"github.com/meridianhub/meridian/shared/platform/bootstrap"
	"github.com/meridianhub/meridian/shared/platform/db"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// ErrRedisURLRequired is returned when Redis fan-out is enabled but no REDIS_URL is configured.
var ErrRedisURLRequired = errors.New("redis fan-out requires REDIS_URL to be configured")

// ErrJWTSigningKeyRequired is returned when SSO is enabled but neither JWT_SIGNING_KEY nor
// JWT_SIGNING_KEY_FILE is set outside local dev mode. Auto-generation would produce
// instance-local keys that break multi-replica deployments and any gateway restart.
var ErrJWTSigningKeyRequired = errors.New("JWT_SIGNING_KEY or JWT_SIGNING_KEY_FILE must be set when SSO is enabled outside local dev mode")

// Build information set via ldflags during compilation.
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

	logger.Info("starting gateway service",
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
	config, err := loadAndValidateConfig(logger)
	if err != nil {
		return err
	}

	// Initialize database pool for tenant resolution and health checks
	dbPool, err := db.NewPostgresPool(context.Background(), db.DefaultConfig(config.DatabaseURL))
	if err != nil {
		return fmt.Errorf("failed to create database pool: %w", err)
	}
	defer func() { _ = dbPool.Close() }()
	logger.Info("database pool initialized")

	// Initialize Redis and health checkers
	redisClient, healthChecker, redisCleanup := initRedisAndHealth(config, dbPool, logger)
	if redisCleanup != nil {
		defer redisCleanup()
	}

	// Build server options
	serverOpts, eventRouter, optCleanups, err := buildServerOptions(config, logger, healthChecker, redisClient)
	// Defer cleanups before checking err: buildServerOptions may return partial
	// cleanups (e.g., ssoCleanup) even when it fails midway through wiring.
	defer func() {
		for _, cleanup := range optCleanups {
			cleanup()
		}
	}()
	if err != nil {
		return err
	}

	// Create server
	server := gateway.NewServer(config, logger, nil, serverOpts...)

	// Start event router and server, then await shutdown
	return startAndServe(logger, server, eventRouter)
}

// loadAndValidateConfig loads gateway configuration and validates it for the current namespace.
func loadAndValidateConfig(logger *slog.Logger) (*gateway.Config, error) {
	config, err := gateway.LoadConfig()
	if err != nil {
		return nil, bootstrap.Permanent(fmt.Errorf("failed to load configuration: %w", err))
	}

	namespace := os.Getenv("POD_NAMESPACE")
	if err := config.ValidateForNamespace(namespace); err != nil {
		return nil, bootstrap.Permanent(err)
	}

	logger.Info("configuration loaded",
		"port", config.Port,
		"base_domain", config.BaseDomain,
		"local_dev_mode", config.LocalDevMode,
		"namespace", namespace,
		"redis_enabled", config.RedisURL != "",
		"backend_routes", len(config.Backends),
		"event_stream_enabled", config.EventStream.Enabled,
		"event_stream_kafka", config.EventStream.KafkaEnabled,
		"event_stream_redis", config.EventStream.RedisEnabled)

	return config, nil
}

// initRedisAndHealth initializes the Redis client (if configured) and builds health checkers.
// Returns a nil redisClient if Redis is not configured. The cleanup function closes the Redis client.
func initRedisAndHealth(config *gateway.Config, dbPool *db.PostgresPool, logger *slog.Logger) (*redis.Client, *gwhealth.GatewayHealthChecker, func()) {
	checkers := []health.Checker{
		health.NewDatabaseChecker(dbPool),
	}

	var redisClient *redis.Client
	var cleanup func()
	if config.RedisURL != "" {
		opt, err := redis.ParseURL(config.RedisURL)
		if err != nil {
			redacted := config.RedisURL
			if u, parseErr := url.Parse(config.RedisURL); parseErr == nil {
				redacted = u.Redacted()
			}
			logger.Warn("redis URL parse failed, falling back to direct addr", "url", redacted, "error", err)
			opt = &redis.Options{Addr: config.RedisURL}
		}
		redisClient = redis.NewClient(opt)
		cleanup = func() { _ = redisClient.Close() }

		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			logger.Warn("redis connection failed (will operate in degraded mode)", "error", err)
		} else {
			logger.Info("redis client initialized")
			checkers = append(checkers, health.NewRedisChecker(redisClient))
		}
	}

	healthChecker := gwhealth.NewGatewayHealthChecker(gwhealth.Config{
		Checkers:     checkers,
		CheckTimeout: 5 * time.Second,
	})

	return redisClient, healthChecker, cleanup
}

// buildServerOptions assembles server options for auth, SSO, and event streaming.
// Returns the options, event router (if any), cleanup functions, and any error.
func buildServerOptions(
	config *gateway.Config,
	logger *slog.Logger,
	healthChecker *gwhealth.GatewayHealthChecker,
	redisClient *redis.Client,
) ([]gateway.ServerOption, *eventstream.Router, []func(), error) {
	serverOpts := []gateway.ServerOption{
		gateway.WithHealthChecker(healthChecker),
	}
	var cleanups []func()
	var eventRouter *eventstream.Router

	if config.Auth.Enabled {
		authMiddleware, err := gateway.BuildAuthMiddleware(config.Auth, logger)
		if err != nil {
			return nil, nil, nil, bootstrap.Permanent(fmt.Errorf("failed to build auth middleware: %w", err))
		}
		if authMiddleware != nil {
			serverOpts = append(serverOpts, gateway.WithAuthMiddleware(authMiddleware))
		}
	}

	ssoOpt, ssoCleanup, err := wireBFFSSO(config, logger)
	if err != nil {
		return nil, nil, nil, bootstrap.Permanent(fmt.Errorf("failed to wire BFF SSO: %w", err))
	}
	if ssoCleanup != nil {
		cleanups = append(cleanups, ssoCleanup)
	}
	if ssoOpt != nil {
		serverOpts = append(serverOpts, ssoOpt)
	}

	if config.EventStream.Enabled {
		router, wsHandler, esCleanup, err := buildEventStreamComponents(config, logger, redisClient)
		if err != nil {
			return nil, nil, cleanups, fmt.Errorf("failed to initialize event streaming: %w", err)
		}
		if esCleanup != nil {
			cleanups = append(cleanups, esCleanup)
		}
		eventRouter = router
		serverOpts = append(serverOpts, gateway.WithEventStreamHandler(wsHandler))
		logger.Info("event streaming initialized",
			"kafka", config.EventStream.KafkaEnabled,
			"redis_fanout", config.EventStream.RedisEnabled)
	}

	return serverOpts, eventRouter, cleanups, nil
}

// startAndServe starts the event router and HTTP server, waits for shutdown, and
// performs graceful cleanup.
func startAndServe(
	logger *slog.Logger,
	server *gateway.Server,
	eventRouter *eventstream.Router,
) error {
	serverErrors := make(chan error, 2)

	routerCtx, routerCancel := context.WithCancel(context.Background())
	defer routerCancel()

	if eventRouter != nil {
		go func() {
			if err := eventRouter.Start(routerCtx); err != nil {
				logger.Error("event router error", "error", err)
				serverErrors <- fmt.Errorf("event router error: %w", err)
			}
		}()
		logger.Info("event router started")
	}

	go func() {
		if err := server.Start(context.Background()); err != nil {
			serverErrors <- err
		}
	}()

	sigChan, signalCleanup := bootstrap.SignalHandler()
	defer signalCleanup()

	var runErr error
	select {
	case sig := <-sigChan:
		logger.Info("received signal", "signal", sig)
	case err := <-serverErrors:
		logger.Error("server error", "error", err)
		runErr = fmt.Errorf("server error: %w", err)
	}

	logger.Info("initiating graceful shutdown...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown error", "error", err)
	}

	return runErr
}

// buildEventStreamComponents constructs the event source, fan-out, router, and WebSocket
// handler based on configuration flags. Returns the router (for lifecycle management),
// the handler (for route registration), an optional cleanup function, and any error.
//
// The caller is responsible for calling router.Start(ctx) in a goroutine to begin
// event consumption, and router.Shutdown(ctx) during graceful shutdown.
//
// Source selection:
//   - KafkaEnabled=true  → KafkaEventSource (production: Kafka topics)
//   - KafkaEnabled=false → OutboxEventSource (dev/CI: polls shared outbox table)
//
// Fan-out selection:
//   - RedisEnabled=true  → RedisFanOut (multi-instance: Redis pub/sub per tenant)
//   - RedisEnabled=false → LocalFanOut (single-instance: in-process channels)
func buildEventStreamComponents(
	config *gateway.Config,
	logger *slog.Logger,
	redisClient *redis.Client,
) (*eventstream.Router, *eventstream.Handler, func(), error) {
	esCfg := config.EventStream

	// Select event source
	var source eventstream.EventSource
	var cleanup func()

	if esCfg.KafkaEnabled {
		kafkaSource, err := adapters.NewKafkaEventSource(
			esCfg.KafkaBrokers,
			esCfg.KafkaTopics,
			logger,
		)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to create kafka event source: %w", err)
		}
		source = kafkaSource
		logger.Info("using kafka event source",
			"brokers", esCfg.KafkaBrokers,
			"topics", esCfg.KafkaTopics)
	} else {
		// Outbox source: requires a GORM DB connection to the shared database.
		// This is the dev/CI adapter; cross-service DB access is forbidden in production (ADR-0002).
		gormDB, err := gorm.Open(postgres.Open(config.DatabaseURL), &gorm.Config{
			SkipDefaultTransaction: true,
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to open gorm DB for outbox source: %w", err)
		}

		sqlDB, err := gormDB.DB()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("failed to get underlying DB for outbox source: %w", err)
		}
		cleanup = func() {
			if err := sqlDB.Close(); err != nil {
				logger.Warn("failed to close outbox DB connection", "error", err)
			}
		}

		outbox := adapters.NewOutboxEventSource(gormDB, esCfg.OutboxPollInterval, logger)
		source = outbox
		logger.Info("using outbox event source (dev/CI mode)",
			"poll_interval", esCfg.OutboxPollInterval)
	}

	// Select fan-out backend
	var fanOut eventstream.FanOut
	if esCfg.RedisEnabled {
		if redisClient == nil {
			return nil, nil, cleanup, ErrRedisURLRequired
		}
		fanOut = adapters.NewRedisFanOut(redisClient, logger)
		logger.Info("using redis fan-out")
	} else {
		fanOut = adapters.NewLocalFanOut(esCfg.BufferSize)
		logger.Info("using local (in-process) fan-out", "buffer_size", esCfg.BufferSize)
	}

	router := eventstream.NewRouter(source, fanOut, eventstream.WithMaxChainDepth(esCfg.MaxChainDepth))
	handler := eventstream.NewHandler(router, logger)

	return router, handler, cleanup, nil
}

// wireBFFSSO creates the BFF SSO handler for OIDC-based login via Dex.
// Returns (nil, nil, nil) when SSO_DEX_ISSUER_URL is unset — SSO is optional.
// The returned cleanup func must be called on shutdown to close the identity DB pool.
//
// Environment variables:
//   - SSO_DEX_ISSUER_URL: Dex issuer URL (e.g., "https://demo.meridianhub.cloud/dex"). Required to enable SSO.
//   - SSO_CLIENT_ID: OAuth client ID (default: "meridian-service")
//   - SSO_CALLBACK_URL: BFF callback URL (e.g., "https://demo.meridianhub.cloud/api/auth/callback")
//   - JWT_SIGNING_KEY: RSA private key in PEM format. Required unless LocalDevMode is enabled.
//   - JWT_SIGNING_KEY_ID: kid header value (default: "meridian-1")
//   - JWT_SIGNING_ISSUER: iss claim value (default: "meridian")
//   - JWT_TOKEN_TTL: token lifetime (default: "1h")
func wireBFFSSO(config *gateway.Config, logger *slog.Logger) (gateway.ServerOption, func(), error) {
	dexIssuerURL := os.Getenv("SSO_DEX_ISSUER_URL")
	if dexIssuerURL == "" {
		logger.Info("SSO_DEX_ISSUER_URL not set, BFF SSO disabled")
		return nil, nil, nil
	}

	privateKeyPEM := os.Getenv("JWT_SIGNING_KEY")
	privateKeyFile := os.Getenv("JWT_SIGNING_KEY_FILE")
	if privateKeyPEM == "" && privateKeyFile == "" && !config.LocalDevMode {
		return nil, nil, ErrJWTSigningKeyRequired
	}

	signer, err := platformauth.NewJWTSigner(platformauth.JWTSignerConfig{
		PrivateKeyFile: privateKeyFile,
		PrivateKeyPEM:  privateKeyPEM,
		KeyID:          env.GetEnvOrDefault("JWT_SIGNING_KEY_ID", "meridian-1"),
		Issuer:         env.GetEnvOrDefault("JWT_SIGNING_ISSUER", "meridian"),
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create JWT signer for SSO: %w", err)
	}

	identityDB, err := gorm.Open(postgres.Open(config.DatabaseURL), &gorm.Config{
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open identity DB for SSO: %w", err)
	}

	cleanup := func() {
		sqlDB, dbErr := identityDB.DB()
		if dbErr != nil {
			logger.Warn("failed to get underlying DB for SSO cleanup", "error", dbErr)
			return
		}
		if closeErr := sqlDB.Close(); closeErr != nil {
			logger.Warn("failed to close SSO identity DB connection", "error", closeErr)
		}
	}

	identityRepo := identitypersistence.NewRepository(identityDB)
	conn, err := identityconnector.New(identityRepo, logger)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to create identity connector for SSO: %w", err)
	}

	tokenTTL := env.GetEnvAsDuration("JWT_TOKEN_TTL", time.Hour)

	handler, err := gateway.NewSSOHandler(gateway.SSOHandlerConfig{
		DexIssuerURL: dexIssuerURL,
		ClientID:     env.GetEnvOrDefault("SSO_CLIENT_ID", "meridian-service"),
		CallbackURL:  os.Getenv("SSO_CALLBACK_URL"),
		Signer:       signer,
		Resolver:     conn,
		TokenTTL:     tokenTTL,
		Logger:       logger,
	})
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("failed to create SSO handler: %w", err)
	}

	logger.Info("BFF SSO handler initialized",
		"dex_issuer_url", dexIssuerURL,
		"client_id", env.GetEnvOrDefault("SSO_CLIENT_ID", "meridian-service"),
		"token_ttl", tokenTTL)

	return gateway.WithSSOHandler(handler), cleanup, nil
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
