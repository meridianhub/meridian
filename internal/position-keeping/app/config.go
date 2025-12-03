// Package app provides application configuration and dependency injection for the position-keeping service.
package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration for the position-keeping service
type Config struct {
	Server        ServerConfig
	Database      DatabaseConfig
	Kafka         KafkaConfig
	Redis         RedisConfig
	Auth          AuthConfig
	Observability ObservabilityConfig
}

// ServerConfig holds gRPC server configuration
type ServerConfig struct {
	// Port is the gRPC server port
	Port string
	// GracefulShutdownTimeout is the maximum time to wait for graceful shutdown
	GracefulShutdownTimeout time.Duration
}

// DatabaseConfig holds PostgreSQL connection configuration
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
	// HealthCheckInterval is the interval for database health checks
	HealthCheckInterval time.Duration
}

// KafkaConfig holds Kafka event publishing configuration
type KafkaConfig struct {
	// Brokers is the list of Kafka broker addresses
	Brokers []string
	// Topic is the Kafka topic for position-keeping events
	Topic string
	// Enabled indicates if event publishing is enabled
	Enabled bool
	// ProducerTimeout is the maximum time to wait for message delivery
	ProducerTimeout time.Duration
}

// RedisConfig holds Redis configuration for caching and rate limiting
type RedisConfig struct {
	// Address is the Redis server address
	Address string
	// Password is the Redis password (optional)
	Password string
	// DB is the Redis database number
	DB int
	// Enabled indicates if Redis features are enabled
	Enabled bool
	// PoolSize is the maximum number of socket connections
	PoolSize int
	// ConnMaxIdleTime is the maximum time a connection may be idle before being closed
	ConnMaxIdleTime time.Duration
}

// AuthConfig holds JWT authentication configuration
type AuthConfig struct {
	// Enabled indicates if JWT authentication is enabled
	Enabled bool
	// JWKSURL is the JWKS endpoint URL for JWT validation
	JWKSURL string
	// JWKSCacheTTL is how long to cache JWKS keys
	JWKSCacheTTL time.Duration
	// JWKSRefreshTTL is the background refresh interval for JWKS
	JWKSRefreshTTL time.Duration
}

// ObservabilityConfig holds observability configuration
type ObservabilityConfig struct {
	// ServiceName is the service name for tracing and metrics
	ServiceName string
	// ServiceVersion is the service version for tracing
	ServiceVersion string
	// Environment is the deployment environment (dev, staging, prod)
	Environment string
	// OTLPEndpoint is the OpenTelemetry collector endpoint
	OTLPEndpoint string
	// SamplingRate is the trace sampling rate (0.0 to 1.0)
	SamplingRate float64
	// LogLevel is the logging level (debug, info, warn, error)
	LogLevel string
	// MetricsEnabled indicates if Prometheus metrics are enabled
	MetricsEnabled bool
	// MetricsPort is the port for Prometheus metrics endpoint
	MetricsPort string
}

// LoadConfig loads configuration from environment variables with defaults
func LoadConfig() (*Config, error) {
	config := &Config{
		Server:        loadServerConfig(),
		Database:      loadDatabaseConfig(),
		Kafka:         loadKafkaConfig(),
		Redis:         loadRedisConfig(),
		Auth:          loadAuthConfig(),
		Observability: loadObservabilityConfig(),
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

// loadServerConfig loads server configuration from environment variables
func loadServerConfig() ServerConfig {
	return ServerConfig{
		Port:                    getEnvOrDefault("GRPC_PORT", "50053"),
		GracefulShutdownTimeout: getEnvAsDuration("GRACEFUL_SHUTDOWN_TIMEOUT", 30*time.Second),
	}
}

// loadDatabaseConfig loads database configuration from environment variables
func loadDatabaseConfig() DatabaseConfig {
	return DatabaseConfig{
		URL:                 strings.TrimSpace(os.Getenv("DATABASE_URL")), // Required - no default to avoid hardcoded credentials
		MaxOpenConns:        getEnvAsInt("DB_MAX_OPEN_CONNS", 25),
		MaxIdleConns:        getEnvAsInt("DB_MAX_IDLE_CONNS", 5),
		ConnMaxLifetime:     getEnvAsDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute),
		ConnMaxIdleTime:     getEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 10*time.Minute),
		HealthCheckInterval: getEnvAsDuration("DB_HEALTH_CHECK_INTERVAL", 30*time.Second),
	}
}

// loadKafkaConfig loads Kafka configuration from environment variables
func loadKafkaConfig() KafkaConfig {
	brokers := getEnvAsSlice("KAFKA_BROKERS", []string{"kafka:9092"})
	enabled := getEnvAsBool("KAFKA_ENABLED", true)

	return KafkaConfig{
		Brokers:         brokers,
		Topic:           getEnvOrDefault("KAFKA_TOPIC", "position-keeping-events"),
		Enabled:         enabled,
		ProducerTimeout: getEnvAsDuration("KAFKA_PRODUCER_TIMEOUT", 10*time.Second),
	}
}

// loadRedisConfig loads Redis configuration from environment variables
func loadRedisConfig() RedisConfig {
	enabled := getEnvAsBool("REDIS_ENABLED", false)

	return RedisConfig{
		Address:         getEnvOrDefault("REDIS_ADDRESS", "redis:6379"),
		Password:        os.Getenv("REDIS_PASSWORD"),
		DB:              getEnvAsInt("REDIS_DB", 0),
		Enabled:         enabled,
		PoolSize:        getEnvAsInt("REDIS_POOL_SIZE", 10),
		ConnMaxIdleTime: getEnvAsDuration("REDIS_CONN_MAX_IDLE_TIME", 5*time.Minute),
	}
}

// loadAuthConfig loads JWT authentication configuration from environment variables
func loadAuthConfig() AuthConfig {
	enabled := getEnvAsBool("AUTH_ENABLED", false)

	return AuthConfig{
		Enabled:        enabled,
		JWKSURL:        getEnvOrDefault("JWKS_URL", "http://localhost:18080/realms/meridian/protocol/openid-connect/certs"),
		JWKSCacheTTL:   getEnvAsDuration("JWKS_CACHE_TTL", 1*time.Hour),
		JWKSRefreshTTL: getEnvAsDuration("JWKS_REFRESH_TTL", 30*time.Minute),
	}
}

// loadObservabilityConfig loads observability configuration from environment variables
func loadObservabilityConfig() ObservabilityConfig {
	return ObservabilityConfig{
		ServiceName:    getEnvOrDefault("SERVICE_NAME", "position-keeping-service"),
		ServiceVersion: getEnvOrDefault("SERVICE_VERSION", "dev"),
		Environment:    getEnvOrDefault("ENVIRONMENT", "development"),
		OTLPEndpoint:   getEnvOrDefault("OTLP_ENDPOINT", ""),
		SamplingRate:   getEnvAsFloat("SAMPLING_RATE", 1.0),
		LogLevel:       getEnvOrDefault("LOG_LEVEL", "info"),
		MetricsEnabled: getEnvAsBool("METRICS_ENABLED", true),
		MetricsPort:    getEnvOrDefault("METRICS_PORT", "9090"),
	}
}

// Validation errors
var (
	ErrEmptyPort              = fmt.Errorf("server port must not be empty")
	ErrEmptyDatabaseURL       = fmt.Errorf("database URL must not be empty")
	ErrInvalidMaxOpenConns    = fmt.Errorf("database max open connections must be at least 1")
	ErrInvalidMaxIdleConns    = fmt.Errorf("database max idle connections must be non-negative")
	ErrKafkaBrokersEmpty      = fmt.Errorf("kafka brokers must not be empty when kafka is enabled")
	ErrInvalidSamplingRate    = fmt.Errorf("sampling rate must be between 0.0 and 1.0")
	ErrContainerCloseFailures = fmt.Errorf("errors during container close")
	ErrMaxOpenConnsOverflow   = fmt.Errorf("max open connections exceeds int32 limit")
	ErrMaxIdleConnsOverflow   = fmt.Errorf("max idle connections exceeds int32 limit")
)

// Validate validates the configuration
func (c *Config) Validate() error {
	if c.Server.Port == "" {
		return ErrEmptyPort
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

	// Validate connection counts fit in int32 range (pgxpool requirement)
	const maxInt32 = 2147483647
	if c.Database.MaxOpenConns > maxInt32 {
		return fmt.Errorf("%w: %d", ErrMaxOpenConnsOverflow, c.Database.MaxOpenConns)
	}
	if c.Database.MaxIdleConns > maxInt32 {
		return fmt.Errorf("%w: %d", ErrMaxIdleConnsOverflow, c.Database.MaxIdleConns)
	}

	if c.Kafka.Enabled && len(c.Kafka.Brokers) == 0 {
		return ErrKafkaBrokersEmpty
	}

	if c.Observability.SamplingRate < 0.0 || c.Observability.SamplingRate > 1.0 {
		return ErrInvalidSamplingRate
	}

	return nil
}

// Helper functions for environment variable parsing

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

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

// getEnvAsFloat returns the environment variable value as float64 or default
func getEnvAsFloat(key string, defaultValue float64) float64 {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return defaultValue
	}
	return value
}

// getEnvAsBool returns the environment variable value as bool or default
func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.ParseBool(valueStr)
	if err != nil {
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

// getEnvAsSlice returns the environment variable value as string slice or default
// Expects comma-separated values with whitespace trimming
func getEnvAsSlice(key string, defaultValue []string) []string {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	var result []string
	parts := strings.Split(valueStr, ",")
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	if len(result) == 0 {
		return defaultValue
	}
	return result
}
