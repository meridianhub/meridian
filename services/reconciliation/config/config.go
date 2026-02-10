// Package config provides configuration for the reconciliation service.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/ports"
)

// Config holds all configuration for the reconciliation service.
type Config struct {
	Server        ServerConfig
	Database      DatabaseConfig
	Kafka         KafkaConfig
	Redis         RedisConfig
	Observability ObservabilityConfig
	Services      ServiceURLsConfig
	Scheduler     SchedulerConfig
}

// ServerConfig holds gRPC server configuration.
type ServerConfig struct {
	// Port is the gRPC server port.
	Port string
	// GracefulShutdownTimeout is the maximum time to wait for graceful shutdown.
	GracefulShutdownTimeout time.Duration
}

// DatabaseConfig holds database connection configuration.
type DatabaseConfig struct {
	// URL is the database connection string.
	URL string
	// MaxOpenConns is the maximum number of open connections to the database.
	MaxOpenConns int
	// MaxIdleConns is the maximum number of idle connections in the pool.
	MaxIdleConns int
	// ConnMaxLifetime is the maximum amount of time a connection may be reused.
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime is the maximum time a connection may be idle before being closed.
	ConnMaxIdleTime time.Duration
}

// KafkaConfig holds Kafka event configuration.
type KafkaConfig struct {
	// Brokers is the list of Kafka broker addresses.
	Brokers string
	// Enabled indicates if Kafka is enabled.
	Enabled bool
}

// RedisConfig holds Redis configuration.
type RedisConfig struct {
	// URL is the Redis connection URL.
	URL string
	// Enabled indicates if Redis is enabled.
	Enabled bool
}

// ObservabilityConfig holds observability configuration.
type ObservabilityConfig struct {
	// ServiceName is the service name for tracing and metrics.
	ServiceName string
	// ServiceVersion is the service version for tracing.
	ServiceVersion string
	// Environment is the deployment environment.
	Environment string
	// MetricsPort is the port for the HTTP metrics and health endpoint.
	MetricsPort string
	// LogLevel is the logging level.
	LogLevel string
}

// ServiceURLsConfig holds URLs for upstream service dependencies.
type ServiceURLsConfig struct {
	// PositionKeepingURL is the gRPC address of the Position Keeping service.
	PositionKeepingURL string
	// FinancialAccountingURL is the gRPC address of the Financial Accounting service.
	FinancialAccountingURL string
	// CurrentAccountURL is the gRPC address of the Current Account service.
	CurrentAccountURL string
	// ReferenceDataURL is the gRPC address of the Reference Data service.
	ReferenceDataURL string
	// PaymentOrderURL is the gRPC address of the Payment Order service.
	PaymentOrderURL string
}

// SchedulerConfig holds configuration for the settlement scheduler.
type SchedulerConfig struct {
	// Enabled indicates if the settlement scheduler is enabled.
	Enabled bool
	// PollInterval is how often to refresh settlement schedules from Reference Data.
	PollInterval time.Duration
	// ShutdownTimeout is the maximum time to wait for in-flight jobs on shutdown.
	ShutdownTimeout time.Duration
	// LeaderLockTTL is the TTL for the Redis leader election lock.
	LeaderLockTTL time.Duration
	// LeaderRenewInterval is how often to renew the leader lock.
	LeaderRenewInterval time.Duration
}

// Validation errors.
var (
	ErrEmptyPort           = errors.New("server port must not be empty")
	ErrEmptyDatabaseURL    = errors.New("database URL must not be empty")
	ErrInvalidMaxOpenConns = errors.New("database max open connections must be at least 1")
	ErrInvalidMaxIdleConns = errors.New("database max idle connections must be non-negative")
	ErrInvalidMetricsPort  = errors.New("metrics port must not be empty")
	ErrInvalidPortNumber   = errors.New("port must be a valid number")
)

// LoadConfig loads configuration from environment variables with defaults.
func LoadConfig() (*Config, error) {
	config := &Config{
		Server:        loadServerConfig(),
		Database:      loadDatabaseConfig(),
		Kafka:         loadKafkaConfig(),
		Redis:         loadRedisConfig(),
		Observability: loadObservabilityConfig(),
		Services:      loadServiceURLsConfig(),
		Scheduler:     loadSchedulerConfig(),
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

func loadServerConfig() ServerConfig {
	return ServerConfig{
		Port:                    env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.Reconciliation)),
		GracefulShutdownTimeout: env.GetEnvAsDuration("GRACEFUL_SHUTDOWN_TIMEOUT", 30*time.Second),
	}
}

func loadDatabaseConfig() DatabaseConfig {
	return DatabaseConfig{
		URL:             env.GetEnvOrDefault("DATABASE_URL", ""),
		MaxOpenConns:    env.GetEnvAsInt("DB_MAX_OPEN_CONNS", 25),
		MaxIdleConns:    env.GetEnvAsInt("DB_MAX_IDLE_CONNS", 5),
		ConnMaxLifetime: env.GetEnvAsDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute),
		ConnMaxIdleTime: env.GetEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 10*time.Minute),
	}
}

func loadKafkaConfig() KafkaConfig {
	brokers := env.GetEnvOrDefault("KAFKA_BROKERS", "")
	return KafkaConfig{
		Brokers: brokers,
		Enabled: brokers != "",
	}
}

func loadRedisConfig() RedisConfig {
	url := env.GetEnvOrDefault("REDIS_URL", "")
	return RedisConfig{
		URL:     url,
		Enabled: url != "",
	}
}

func loadObservabilityConfig() ObservabilityConfig {
	return ObservabilityConfig{
		ServiceName:    "reconciliation-service",
		ServiceVersion: "dev",
		Environment:    env.GetEnvOrDefault("ENVIRONMENT", "development"),
		MetricsPort:    env.GetEnvOrDefault("METRICS_PORT", "9090"),
		LogLevel:       env.GetEnvOrDefault("LOG_LEVEL", "info"),
	}
}

func loadServiceURLsConfig() ServiceURLsConfig {
	return ServiceURLsConfig{
		PositionKeepingURL:     env.GetEnvOrDefault("POSITION_KEEPING_URL", ""),
		FinancialAccountingURL: env.GetEnvOrDefault("FINANCIAL_ACCOUNTING_URL", ""),
		CurrentAccountURL:      env.GetEnvOrDefault("CURRENT_ACCOUNT_URL", ""),
		ReferenceDataURL:       env.GetEnvOrDefault("REFERENCE_DATA_URL", ""),
		PaymentOrderURL:        env.GetEnvOrDefault("PAYMENT_ORDER_URL", ""),
	}
}

func loadSchedulerConfig() SchedulerConfig {
	return SchedulerConfig{
		Enabled:             env.GetEnvAsBool("SETTLEMENT_SCHEDULER_ENABLED", false),
		PollInterval:        env.GetEnvAsDuration("SCHEDULER_POLL_INTERVAL", 1*time.Hour),
		ShutdownTimeout:     env.GetEnvAsDuration("SCHEDULER_SHUTDOWN_TIMEOUT", 30*time.Second),
		LeaderLockTTL:       env.GetEnvAsDuration("SCHEDULER_LEADER_LOCK_TTL", 30*time.Second),
		LeaderRenewInterval: env.GetEnvAsDuration("SCHEDULER_LEADER_RENEW_INTERVAL", 10*time.Second),
	}
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	if c.Server.Port == "" {
		return ErrEmptyPort
	}
	if _, err := strconv.Atoi(c.Server.Port); err != nil {
		return fmt.Errorf("server %w: %w", ErrInvalidPortNumber, err)
	}
	if c.Database.URL == "" {
		return ErrEmptyDatabaseURL
	}
	if c.Database.MaxOpenConns < 1 {
		return ErrInvalidMaxOpenConns
	}
	if c.Database.MaxIdleConns < 0 {
		return ErrInvalidMaxIdleConns
	}
	if c.Observability.MetricsPort == "" {
		return ErrInvalidMetricsPort
	}
	if _, err := strconv.Atoi(c.Observability.MetricsPort); err != nil {
		return fmt.Errorf("metrics %w: %w", ErrInvalidPortNumber, err)
	}
	return nil
}
