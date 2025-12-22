package app

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/meridianhub/meridian/shared/platform/audit"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Container holds all application dependencies.
type Container struct {
	Config *Config
	Logger *slog.Logger

	// Infrastructure
	DB *gorm.DB

	// Audit consumer
	AuditConsumer *audit.Consumer
}

// ContainerCloseError is returned when multiple errors occur during container close.
type ContainerCloseError struct {
	Errors []error
}

func (e *ContainerCloseError) Error() string {
	return fmt.Sprintf("errors during container close: %d errors", len(e.Errors))
}

// NewContainer creates and initializes a new dependency injection container.
func NewContainer(ctx context.Context, config *Config, logger *slog.Logger) (*Container, error) {
	container := &Container{
		Config: config,
		Logger: logger,
	}

	// Initialize dependencies in order
	if err := container.initializeDatabase(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	if err := container.initializeAuditConsumer(); err != nil {
		// Clean up already initialized resources
		_ = container.Close(ctx)
		return nil, fmt.Errorf("failed to initialize audit consumer: %w", err)
	}

	logger.Info("dependency container initialized successfully")

	return container, nil
}

// initializeDatabase initializes the database connection using GORM.
func (c *Container) initializeDatabase(ctx context.Context) error {
	// Initialize GORM with PostgreSQL driver
	gormDB, err := gorm.Open(postgres.Open(c.Config.Database.URL), &gorm.Config{
		// Disable default transaction for performance
		SkipDefaultTransaction: true,
	})
	if err != nil {
		return fmt.Errorf("failed to initialize GORM: %w", err)
	}

	// Configure connection pool settings
	sqlDB, err := gormDB.DB()
	if err != nil {
		return fmt.Errorf("failed to get database instance: %w", err)
	}

	// Apply connection pool configuration
	sqlDB.SetMaxOpenConns(c.Config.Database.MaxOpenConns)
	sqlDB.SetMaxIdleConns(c.Config.Database.MaxIdleConns)
	sqlDB.SetConnMaxLifetime(c.Config.Database.ConnMaxLifetime)
	sqlDB.SetConnMaxIdleTime(c.Config.Database.ConnMaxIdleTime)

	// Verify connection
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	c.DB = gormDB
	c.Logger.Info("database connection initialized",
		"max_open_conns", c.Config.Database.MaxOpenConns,
		"max_idle_conns", c.Config.Database.MaxIdleConns,
		"conn_max_lifetime", c.Config.Database.ConnMaxLifetime,
		"conn_max_idle_time", c.Config.Database.ConnMaxIdleTime)

	return nil
}

// initializeAuditConsumer initializes the Kafka audit consumer with tenant audit writer.
func (c *Container) initializeAuditConsumer() error {
	// Create audit consumer configuration
	consumerConfig := audit.ConsumerConfig{
		BootstrapServers: c.Config.Kafka.BootstrapServers,
		GroupID:          c.Config.Kafka.GroupID,
		ClientID:         c.Config.Kafka.ClientID,
		Topic:            c.Config.Kafka.Topic,
		DB:               c.DB,
		HandlerTimeout:   c.Config.Kafka.HandlerTimeout,
		MaxRetries:       c.Config.Kafka.MaxRetries,
	}

	consumer, err := audit.NewConsumer(consumerConfig)
	if err != nil {
		return fmt.Errorf("failed to create audit consumer: %w", err)
	}

	c.AuditConsumer = consumer
	c.Logger.Info("audit consumer initialized",
		"bootstrap_servers", c.Config.Kafka.BootstrapServers,
		"topic", c.Config.Kafka.Topic,
		"group_id", c.Config.Kafka.GroupID,
		"service_name", c.Config.Service.Name)

	return nil
}

// Close gracefully closes all resources in the container.
func (c *Container) Close(_ context.Context) error {
	c.Logger.Info("closing container resources...")

	var errs []error

	// Close audit consumer first (stop consuming before closing DB)
	if c.AuditConsumer != nil {
		if err := c.AuditConsumer.Close(); err != nil {
			c.Logger.Error("failed to close audit consumer", "error", err)
			errs = append(errs, fmt.Errorf("audit consumer close: %w", err))
		} else {
			c.Logger.Info("audit consumer closed")
		}
	}

	// Close database connection
	if c.DB != nil {
		sqlDB, err := c.DB.DB()
		if err != nil {
			c.Logger.Error("failed to get database instance for close", "error", err)
			errs = append(errs, fmt.Errorf("database get instance: %w", err))
		} else if err := sqlDB.Close(); err != nil {
			c.Logger.Error("failed to close database", "error", err)
			errs = append(errs, fmt.Errorf("database close: %w", err))
		} else {
			c.Logger.Info("database connection closed")
		}
	}

	if len(errs) > 0 {
		return &ContainerCloseError{Errors: errs}
	}

	c.Logger.Info("container resources closed successfully")
	return nil
}
