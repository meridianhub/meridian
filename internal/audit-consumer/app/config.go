// Package app provides application configuration and dependency injection for the audit-consumer service.
package app

import (
	"fmt"
	"log"
	"math"
	"os"
	"strconv"
	"time"
)

// Config holds all configuration for the audit-consumer service.
type Config struct {
	Service  ServiceConfig
	Database DatabaseConfig
	Kafka    KafkaConfig
}

// ServiceConfig holds service-level configuration.
type ServiceConfig struct {
	// Name identifies which service this consumer is processing events for
	// (e.g., "current-account", "financial-accounting", "position-keeping")
	Name string
	// Port is the HTTP server port for health checks and metrics
	Port string
	// GracefulShutdownTimeout is the maximum time to wait for graceful shutdown
	GracefulShutdownTimeout time.Duration
}

// DatabaseConfig holds PostgreSQL connection configuration.
type DatabaseConfig struct {
	// URL is the PostgreSQL connection string
	URL string
	// MaxOpenConns is the maximum number of open connections to the database
	MaxOpenConns int
	// MaxIdleConns is the maximum number of idle connections in the pool
	MaxIdleConns int
	// ConnMaxLifetime is the maximum amount of time a connection may be reused
	ConnMaxLifetime time.Duration
	// ConnMaxIdleTime is the maximum time a connection may be idle before being closed
	ConnMaxIdleTime time.Duration
	// PoolStatsInterval is the interval for collecting connection pool statistics
	PoolStatsInterval time.Duration
}

// KafkaConfig holds Kafka consumer configuration.
type KafkaConfig struct {
	// BootstrapServers is the comma-separated list of Kafka broker addresses
	BootstrapServers string
	// Topic is the Kafka topic to consume audit events from
	// (e.g., "audit.events.current-account", "audit.events.financial-accounting")
	Topic string
	// GroupID is the consumer group ID for this consumer
	GroupID string
	// ClientID identifies the consumer for logging and metrics
	ClientID string
	// HandlerTimeout is the maximum duration for processing a single message
	HandlerTimeout time.Duration
	// MaxRetries is the maximum number of retry attempts before sending to DLQ
	MaxRetries int
}

// Validation errors
var (
	ErrEmptyServiceName      = fmt.Errorf("service name must not be empty")
	ErrEmptyPort             = fmt.Errorf("service port must not be empty")
	ErrEmptyDatabaseURL      = fmt.Errorf("database URL must not be empty")
	ErrInvalidMaxOpenConns   = fmt.Errorf("database max open connections must be at least 1")
	ErrInvalidMaxIdleConns   = fmt.Errorf("database max idle connections must be non-negative")
	ErrEmptyBootstrapServers = fmt.Errorf("kafka bootstrap servers must not be empty")
	ErrEmptyTopic            = fmt.Errorf("kafka topic must not be empty")
	ErrEmptyGroupID          = fmt.Errorf("kafka group ID must not be empty")
)

// LoadConfig loads configuration from environment variables with defaults.
func LoadConfig() (*Config, error) {
	config := &Config{
		Service:  loadServiceConfig(),
		Database: loadDatabaseConfig(),
		Kafka:    loadKafkaConfig(),
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

// loadServiceConfig loads service configuration from environment variables.
func loadServiceConfig() ServiceConfig {
	return ServiceConfig{
		Name:                    os.Getenv("SERVICE_NAME"), // Required - identifies which service's audit events to process
		Port:                    getEnvOrDefault("PORT", "8080"),
		GracefulShutdownTimeout: getEnvAsDuration("GRACEFUL_SHUTDOWN_TIMEOUT", 30*time.Second),
	}
}

// loadDatabaseConfig loads database configuration from environment variables.
func loadDatabaseConfig() DatabaseConfig {
	return DatabaseConfig{
		URL:               os.Getenv("DATABASE_URL"), // Required - no default to avoid hardcoded credentials
		MaxOpenConns:      getEnvAsInt("DB_MAX_OPEN_CONNS", 25),
		MaxIdleConns:      getEnvAsInt("DB_MAX_IDLE_CONNS", 5),
		ConnMaxLifetime:   getEnvAsDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute),
		ConnMaxIdleTime:   getEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 10*time.Minute),
		PoolStatsInterval: getEnvAsDuration("DB_POOL_STATS_INTERVAL", 10*time.Second),
	}
}

// loadKafkaConfig loads Kafka configuration from environment variables.
func loadKafkaConfig() KafkaConfig {
	return KafkaConfig{
		BootstrapServers: os.Getenv("KAFKA_BOOTSTRAP_SERVERS"), // Required
		Topic:            os.Getenv("AUDIT_TOPIC"),             // Required
		GroupID:          getEnvOrDefault("KAFKA_GROUP_ID", "audit-consumer-group"),
		ClientID:         getEnvOrDefault("KAFKA_CLIENT_ID", "audit-consumer"),
		HandlerTimeout:   getEnvAsDuration("KAFKA_HANDLER_TIMEOUT", 30*time.Second),
		MaxRetries:       getEnvAsInt("KAFKA_MAX_RETRIES", 3),
	}
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	// Service validation
	if c.Service.Name == "" {
		return ErrEmptyServiceName
	}
	if c.Service.Port == "" {
		return ErrEmptyPort
	}

	// Database validation
	if c.Database.URL == "" {
		return ErrEmptyDatabaseURL
	}
	if c.Database.MaxOpenConns < 1 {
		return ErrInvalidMaxOpenConns
	}
	if c.Database.MaxIdleConns < 0 {
		return ErrInvalidMaxIdleConns
	}
	// Note: int32 bounds checking for MaxOpenConns and MaxIdleConns happens in getEnvAsInt()

	// Kafka validation
	if c.Kafka.BootstrapServers == "" {
		return ErrEmptyBootstrapServers
	}
	if c.Kafka.Topic == "" {
		return ErrEmptyTopic
	}
	if c.Kafka.GroupID == "" {
		return ErrEmptyGroupID
	}

	return nil
}

// Helper functions for environment variable parsing

// getEnvOrDefault returns the environment variable value or default.
func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// getEnvAsInt returns the environment variable value as int or default.
// For values that will be converted to int32, ensures they fit within int32 bounds.
func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	// Parse as int64 first to check bounds before converting to int
	value64, err := strconv.ParseInt(valueStr, 10, 64)
	if err != nil {
		log.Printf("WARNING: Invalid integer format for %s=%q, using default value %d: %v", key, valueStr, defaultValue, err)
		return defaultValue
	}

	// For architecture-independent safety, ensure value fits in int32
	// This prevents issues when converting to int32 in downstream code
	if value64 < math.MinInt32 || value64 > math.MaxInt32 {
		log.Printf("WARNING: Value for %s=%d exceeds int32 bounds, using default value %d", key, value64, defaultValue)
		return defaultValue
	}

	return int(value64)
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
