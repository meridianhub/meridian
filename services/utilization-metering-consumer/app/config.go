// Package app provides application-level configuration and dependency injection.
package app

import (
	"errors"
	"fmt"
	"os"
)

// Configuration validation errors
var (
	// ErrKafkaBootstrapServersRequired is returned when KAFKA_BOOTSTRAP_SERVERS is not set
	ErrKafkaBootstrapServersRequired = errors.New("KAFKA_BOOTSTRAP_SERVERS environment variable is required")
	// ErrConsumerGroupIDRequired is returned when CONSUMER_GROUP_ID is not set
	ErrConsumerGroupIDRequired = errors.New("CONSUMER_GROUP_ID environment variable is required")
	// ErrPositionKeepingEndpointRequired is returned when POSITION_KEEPING_ENDPOINT is not set
	ErrPositionKeepingEndpointRequired = errors.New("POSITION_KEEPING_ENDPOINT environment variable is required")
	// ErrTenantZeroIDRequired is returned when TENANT_ZERO_ID is not set
	ErrTenantZeroIDRequired = errors.New("TENANT_ZERO_ID environment variable is required")
)

// Config holds the configuration for the utilization-metering-consumer service.
type Config struct {
	// Kafka configuration
	KafkaBootstrapServers string   // Required: Kafka broker addresses (e.g., "kafka:9092")
	ConsumerGroupID       string   // Required: Consumer group ID for offset management
	AuditTopics           []string // Audit events topics to consume from

	// Position Keeping gRPC endpoint
	PositionKeepingEndpoint string // Required: gRPC endpoint for Position Keeping service (e.g., "position-keeping:50051")

	// Tenant Zero configuration
	TenantZeroID string // Required: Tenant ID for Meridian's platform billing tenant

	// HTTP server configuration
	HTTPPort string // HTTP port for health checks and metrics (default: "8080")
}

// LoadConfig loads configuration from environment variables.
// Returns an error if required configuration is missing.
func LoadConfig() (*Config, error) {
	// Default audit topics for all 6 services
	defaultAuditTopics := []string{
		"current-account.audit.events",
		"financial-accounting.audit.events",
		"position-keeping.audit.events",
		"party.audit.events",
		"payment-order.audit.events",
		"tenant.audit.events",
	}

	config := &Config{
		KafkaBootstrapServers:   getEnv("KAFKA_BOOTSTRAP_SERVERS"),
		ConsumerGroupID:         getEnvOrDefault("CONSUMER_GROUP_ID", "utilization-metering-consumer"),
		AuditTopics:             defaultAuditTopics, // Use default topics
		PositionKeepingEndpoint: getEnv("POSITION_KEEPING_ENDPOINT"),
		TenantZeroID:            getEnv("TENANT_ZERO_ID"),
		HTTPPort:                getEnvOrDefault("HTTP_PORT", "8080"),
	}

	// Validate required configuration
	if config.KafkaBootstrapServers == "" {
		return nil, ErrKafkaBootstrapServersRequired
	}
	if config.ConsumerGroupID == "" {
		return nil, ErrConsumerGroupIDRequired
	}
	if config.PositionKeepingEndpoint == "" {
		return nil, ErrPositionKeepingEndpointRequired
	}
	if config.TenantZeroID == "" {
		return nil, ErrTenantZeroIDRequired
	}

	// Validate tenant zero ID format (basic UUID check)
	if len(config.TenantZeroID) != 36 || config.TenantZeroID[8] != '-' || config.TenantZeroID[13] != '-' {
		return nil, fmt.Errorf("%w: got %s", ErrTenantZeroIDRequired, config.TenantZeroID)
	}

	return config, nil
}

// getEnv returns the value of an environment variable.
func getEnv(key string) string {
	return os.Getenv(key)
}

// getEnvOrDefault returns the value of an environment variable or a default value.
func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
