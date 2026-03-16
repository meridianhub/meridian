// Package app provides application configuration and dependency injection for the position-keeping service.
package app

import (
	"fmt"
	"strconv"
	"time"

	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/ports"
)

// Config holds all configuration for the position-keeping service
type Config struct {
	Server            ServerConfig
	Database          DatabaseConfig
	Kafka             KafkaConfig
	Redis             RedisConfig
	Auth              AuthConfig
	Observability     ObservabilityConfig
	Compaction        CompactionConfig
	AccountValidation AccountValidationConfig
	ReferenceData     ReferenceDataConfig
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

// CompactionConfig holds background compaction worker configuration
type CompactionConfig struct {
	// Enabled indicates if the compaction worker is enabled
	Enabled bool
	// RunInterval is how often the compaction worker runs (e.g., 5 minutes)
	RunInterval time.Duration
	// FragmentThreshold is the minimum number of rows in a bucket to trigger compaction
	FragmentThreshold int
	// BatchSize is the maximum number of buckets to compact per run
	BatchSize int
}

// AccountValidationConfig holds account validation configuration
type AccountValidationConfig struct {
	// Enabled indicates if account validation is enabled
	// When enabled, the service validates that accounts exist in Current Account
	// or Internal Account before creating position logs.
	// Defaults to true to prevent orphan position logs.
	Enabled bool
	// CurrentAccountServiceURL is the gRPC address of the Current Account service
	// Optional - if not specified, Current Account validation is skipped
	CurrentAccountServiceURL string
	// InternalAccountServiceURL is the gRPC address of the Internal Account service
	// Optional - if not specified, Internal Account validation is skipped
	InternalAccountServiceURL string
	// CacheTTL is how long to cache validation results
	// Defaults to 1 minute if not specified
	CacheTTL time.Duration
	// ConnectionTimeout is the timeout for connecting to account services
	// Defaults to 5 seconds if not specified
	ConnectionTimeout time.Duration
}

// ReferenceDataConfig holds Reference Data service connection configuration.
type ReferenceDataConfig struct {
	// ServiceURL is the gRPC address of the Reference Data service.
	// Optional - if not specified, instrument resolution is unavailable.
	ServiceURL string
}

// LoadConfig loads configuration from environment variables with defaults
func LoadConfig() (*Config, error) {
	config := &Config{
		Server:            loadServerConfig(),
		Database:          loadDatabaseConfig(),
		Kafka:             loadKafkaConfig(),
		Redis:             loadRedisConfig(),
		Auth:              loadAuthConfig(),
		Observability:     loadObservabilityConfig(),
		Compaction:        loadCompactionConfig(),
		AccountValidation: loadAccountValidationConfig(),
		ReferenceData:     loadReferenceDataConfig(),
	}

	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return config, nil
}

// loadServerConfig loads server configuration from environment variables
func loadServerConfig() ServerConfig {
	return ServerConfig{
		Port:                    env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.PositionKeeping)),
		GracefulShutdownTimeout: env.GetEnvAsDuration("GRACEFUL_SHUTDOWN_TIMEOUT", 30*time.Second),
	}
}

// loadDatabaseConfig loads database configuration from environment variables
func loadDatabaseConfig() DatabaseConfig {
	return DatabaseConfig{
		URL:                 env.GetEnvOrDefault("DATABASE_URL", ""), // Required - no default to avoid hardcoded credentials
		MaxOpenConns:        env.GetEnvAsInt("DB_MAX_OPEN_CONNS", 25),
		MaxIdleConns:        env.GetEnvAsInt("DB_MAX_IDLE_CONNS", 5),
		ConnMaxLifetime:     env.GetEnvAsDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute),
		ConnMaxIdleTime:     env.GetEnvAsDuration("DB_CONN_MAX_IDLE_TIME", 10*time.Minute),
		HealthCheckInterval: env.GetEnvAsDuration("DB_HEALTH_CHECK_INTERVAL", 30*time.Second),
	}
}

// loadKafkaConfig loads Kafka configuration from environment variables
func loadKafkaConfig() KafkaConfig {
	brokers := env.GetEnvAsSlice("KAFKA_BROKERS", []string{"kafka:9092"})
	enabled := env.GetEnvAsBool("KAFKA_ENABLED", true)

	return KafkaConfig{
		Brokers:         brokers,
		Topic:           env.GetEnvOrDefault("KAFKA_TOPIC", "position-keeping-events"),
		Enabled:         enabled,
		ProducerTimeout: env.GetEnvAsDuration("KAFKA_PRODUCER_TIMEOUT", 10*time.Second),
	}
}

// loadRedisConfig loads Redis configuration from environment variables
func loadRedisConfig() RedisConfig {
	enabled := env.GetEnvAsBool("REDIS_ENABLED", false)

	return RedisConfig{
		Address:         env.GetEnvOrDefault("REDIS_ADDRESS", "redis:6379"),
		Password:        env.GetEnvOrDefault("REDIS_PASSWORD", ""),
		DB:              env.GetEnvAsInt("REDIS_DB", 0),
		Enabled:         enabled,
		PoolSize:        env.GetEnvAsInt("REDIS_POOL_SIZE", 10),
		ConnMaxIdleTime: env.GetEnvAsDuration("REDIS_CONN_MAX_IDLE_TIME", 5*time.Minute),
	}
}

// loadAuthConfig loads JWT authentication configuration from environment variables
func loadAuthConfig() AuthConfig {
	enabled := env.GetEnvAsBool("AUTH_ENABLED", true)

	return AuthConfig{
		Enabled:        enabled,
		JWKSURL:        env.GetEnvOrDefault("JWKS_URL", "http://localhost:18080/realms/meridian/protocol/openid-connect/certs"),
		JWKSCacheTTL:   env.GetEnvAsDuration("JWKS_CACHE_TTL", 1*time.Hour),
		JWKSRefreshTTL: env.GetEnvAsDuration("JWKS_REFRESH_TTL", 30*time.Minute),
	}
}

// loadObservabilityConfig loads observability configuration from environment variables
func loadObservabilityConfig() ObservabilityConfig {
	return ObservabilityConfig{
		ServiceName:    env.GetEnvOrDefault("SERVICE_NAME", "position-keeping-service"),
		ServiceVersion: env.GetEnvOrDefault("SERVICE_VERSION", "dev"),
		Environment:    env.GetEnvOrDefault("ENVIRONMENT", "development"),
		OTLPEndpoint:   env.GetEnvOrDefault("OTLP_ENDPOINT", ""),
		SamplingRate:   env.GetEnvAsFloat("SAMPLING_RATE", 1.0),
		LogLevel:       env.GetEnvOrDefault("LOG_LEVEL", "info"),
		MetricsEnabled: env.GetEnvAsBool("METRICS_ENABLED", true),
		MetricsPort:    env.GetEnvOrDefault("METRICS_PORT", "9090"),
	}
}

// loadCompactionConfig loads background compaction worker configuration from environment variables
func loadCompactionConfig() CompactionConfig {
	return CompactionConfig{
		Enabled:           env.GetEnvAsBool("COMPACTION_ENABLED", true),
		RunInterval:       env.GetEnvAsDuration("COMPACTION_RUN_INTERVAL", 5*time.Minute),
		FragmentThreshold: env.GetEnvAsInt("COMPACTION_FRAGMENT_THRESHOLD", 100),
		BatchSize:         env.GetEnvAsInt("COMPACTION_BATCH_SIZE", 50),
	}
}

// loadAccountValidationConfig loads account validation configuration from environment variables
func loadAccountValidationConfig() AccountValidationConfig {
	return AccountValidationConfig{
		Enabled:                  env.GetEnvAsBool("ACCOUNT_VALIDATION_ENABLED", true),
		CurrentAccountServiceURL: env.GetEnvOrDefault("CURRENT_ACCOUNT_SERVICE_URL", ""),
		InternalAccountServiceURL: env.GetEnvOrDefault("INTERNAL_ACCOUNT_SERVICE_URL",
			env.GetEnvOrDefault("INTERNAL_BANK_ACCOUNT_SERVICE_URL", "")),
		CacheTTL:          env.GetEnvAsDuration("ACCOUNT_VALIDATION_CACHE_TTL", 1*time.Minute),
		ConnectionTimeout: env.GetEnvAsDuration("ACCOUNT_VALIDATION_CONNECTION_TIMEOUT", 5*time.Second),
	}
}

// loadReferenceDataConfig loads Reference Data service configuration from environment variables.
func loadReferenceDataConfig() ReferenceDataConfig {
	return ReferenceDataConfig{
		ServiceURL: env.GetEnvOrDefault("REFERENCE_DATA_SERVICE_URL", ""),
	}
}

// Validation errors
var (
	ErrEmptyPort                  = fmt.Errorf("server port must not be empty")
	ErrEmptyDatabaseURL           = fmt.Errorf("database URL must not be empty")
	ErrInvalidMaxOpenConns        = fmt.Errorf("database max open connections must be at least 1")
	ErrInvalidMaxIdleConns        = fmt.Errorf("database max idle connections must be non-negative")
	ErrKafkaBrokersEmpty          = fmt.Errorf("kafka brokers must not be empty when kafka is enabled")
	ErrInvalidSamplingRate        = fmt.Errorf("sampling rate must be between 0.0 and 1.0")
	ErrContainerCloseFailures     = fmt.Errorf("errors during container close")
	ErrMaxOpenConnsOverflow       = fmt.Errorf("max open connections exceeds int32 limit")
	ErrMaxIdleConnsOverflow       = fmt.Errorf("max idle connections exceeds int32 limit")
	ErrInvalidCompactionInterval  = fmt.Errorf("compaction run interval must be greater than zero")
	ErrInvalidFragmentThreshold   = fmt.Errorf("compaction fragment threshold must be at least 2")
	ErrInvalidCompactionBatchSize = fmt.Errorf("compaction batch size must be at least 1")
	// ErrAccountValidationURLRequired is returned when account validation is enabled but no service URL is provided
	ErrAccountValidationURLRequired = fmt.Errorf("at least one account service URL is required when account validation is enabled")
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

	if err := c.Compaction.Validate(); err != nil {
		return err
	}

	return c.AccountValidation.Validate()
}

// Validate validates the compaction configuration
func (c *CompactionConfig) Validate() error {
	if !c.Enabled {
		return nil
	}
	if c.RunInterval <= 0 {
		return ErrInvalidCompactionInterval
	}
	if c.FragmentThreshold < 2 {
		return ErrInvalidFragmentThreshold
	}
	if c.BatchSize < 1 {
		return ErrInvalidCompactionBatchSize
	}
	return nil
}

// Validate validates the account validation configuration
func (c *AccountValidationConfig) Validate() error {
	if c.Enabled && c.CurrentAccountServiceURL == "" && c.InternalAccountServiceURL == "" {
		return ErrAccountValidationURLRequired
	}
	return nil
}
