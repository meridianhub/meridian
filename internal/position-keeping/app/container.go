package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meridianhub/meridian/internal/platform/observability"
	"github.com/meridianhub/meridian/internal/position-keeping/domain"
	"github.com/meridianhub/meridian/internal/position-keeping/repository"
)

// Container holds all application dependencies
type Container struct {
	Config *Config
	Logger *slog.Logger

	// Infrastructure
	DBPool *pgxpool.Pool
	Tracer *observability.Tracer

	// Adapters
	EventPublisher domain.EventPublisher

	// Repository
	PositionLogRepository domain.FinancialPositionLogRepository
}

// NewContainer creates and initializes a new dependency injection container
func NewContainer(ctx context.Context, config *Config, logger *slog.Logger) (*Container, error) {
	container := &Container{
		Config: config,
		Logger: logger,
	}

	// Initialize dependencies in order
	if err := container.initializeTracer(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize tracer: %w", err)
	}

	if err := container.initializeDatabase(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	container.initializeEventPublisher()

	container.initializeRepositories()

	logger.Info("dependency container initialized successfully")

	return container, nil
}

// initializeTracer initializes the OpenTelemetry tracer
func (c *Container) initializeTracer(ctx context.Context) error {
	// If OTLP endpoint is empty, tracing is disabled
	if c.Config.Observability.OTLPEndpoint == "" {
		c.Logger.Info("tracing disabled (no OTLP endpoint configured)")
		return nil
	}

	tracerConfig, err := observability.DefaultConfig()
	if err != nil {
		return fmt.Errorf("failed to create tracer config: %w", err)
	}

	// Override with our configuration
	tracerConfig = tracerConfig.
		WithServiceName(c.Config.Observability.ServiceName).
		WithServiceVersion(c.Config.Observability.ServiceVersion).
		WithEnvironment(c.Config.Observability.Environment).
		WithOTLPEndpoint(c.Config.Observability.OTLPEndpoint).
		WithSamplingRate(c.Config.Observability.SamplingRate)

	tracer, err := observability.NewTracer(ctx, tracerConfig)
	if err != nil {
		return fmt.Errorf("failed to create tracer: %w", err)
	}

	c.Tracer = tracer
	c.Logger.Info("tracer initialized",
		"service_name", tracerConfig.ServiceName,
		"environment", tracerConfig.Environment,
		"sampling_rate", tracerConfig.SamplingRate,
		"otlp_configured", tracerConfig.OTLPEndpoint != "")

	return nil
}

// initializeDatabase initializes the database connection pool
func (c *Container) initializeDatabase(ctx context.Context) error {
	poolConfig, err := pgxpool.ParseConfig(c.Config.Database.URL)
	if err != nil {
		return fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Configure connection pool
	// #nosec G115 -- overflow validated in config.Validate()
	poolConfig.MaxConns = int32(c.Config.Database.MaxOpenConns)
	// #nosec G115 -- overflow validated in config.Validate()
	poolConfig.MinConns = int32(c.Config.Database.MaxIdleConns)
	poolConfig.MaxConnLifetime = c.Config.Database.ConnMaxLifetime
	poolConfig.MaxConnIdleTime = c.Config.Database.ConnMaxIdleTime
	poolConfig.HealthCheckPeriod = c.Config.Database.HealthCheckInterval

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("failed to create database pool: %w", err)
	}

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return fmt.Errorf("failed to ping database: %w", err)
	}

	c.DBPool = pool
	c.Logger.Info("database pool initialized",
		"max_conns", poolConfig.MaxConns,
		"min_conns", poolConfig.MinConns,
		"max_lifetime", poolConfig.MaxConnLifetime,
		"max_idle_time", poolConfig.MaxConnIdleTime)

	return nil
}

// initializeEventPublisher initializes Kafka event publisher or no-op publisher
func (c *Container) initializeEventPublisher() {
	if !c.Config.Kafka.Enabled {
		c.Logger.Info("kafka disabled, using no-op event publisher")
		c.EventPublisher = domain.NewNoOpEventPublisher()
		return
	}

	// For now, use no-op publisher until Kafka integration is fully wired
	// TODO: Implement Kafka producer initialization when Kafka config is ready
	c.Logger.Info("kafka producer not yet implemented, using no-op event publisher")
	c.EventPublisher = domain.NewNoOpEventPublisher()
}

// initializeRepositories initializes domain repositories
func (c *Container) initializeRepositories() {
	// Create PostgreSQL repository
	c.PositionLogRepository = repository.NewPostgresRepository(c.DBPool)

	c.Logger.Info("repositories initialized")
}

// Close gracefully closes all resources in the container
func (c *Container) Close(ctx context.Context) error {
	c.Logger.Info("closing container resources...")

	var errs []error

	// Close database pool
	if c.DBPool != nil {
		c.DBPool.Close()
		c.Logger.Info("database pool closed")
	}

	// Shutdown tracer
	if c.Tracer != nil {
		if err := c.Tracer.Shutdown(ctx); err != nil {
			c.Logger.Error("failed to shutdown tracer", "error", err)
			errs = append(errs, fmt.Errorf("tracer shutdown: %w", err))
		} else {
			c.Logger.Info("tracer shutdown complete")
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("%w: %v", ErrContainerCloseFailures, errs)
	}

	c.Logger.Info("container resources closed successfully")
	return nil
}
