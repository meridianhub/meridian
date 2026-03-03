// Package app provides application-level configuration and dependency injection.
package app

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/meridianhub/meridian/shared/platform/env"
)

// Configuration validation errors
var (
	// ErrKafkaBootstrapServersRequired is returned when KAFKA_BOOTSTRAP_SERVERS is not set
	ErrKafkaBootstrapServersRequired = errors.New("KAFKA_BOOTSTRAP_SERVERS environment variable is required")
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

	// Tenant-to-Account Mapping
	TenantAccountMapping string // Optional: JSON mapping of tenant UUIDs to account UUIDs

	// HTTP server configuration
	HTTPPort string // HTTP port for health checks and metrics (default: "8080")

	// MDS (Market Data Service) configuration
	EnableMDSOutput      bool          // Feature flag for MDS output (default: true)
	MDSServiceAddr       string        // gRPC address for Market Data Service (e.g., "market-information:50058")
	MDSAggregationWindow time.Duration // Aggregation window size (default: 1h)
	MDSFlushInterval     time.Duration // Flush interval for buffered observations (default: 5m)
}

// LoadConfig loads configuration from environment variables.
// Returns an error if required configuration is missing.
func LoadConfig() (*Config, error) {
	// Default audit topics for all 6 services (service-name.event-name.v1 convention)
	defaultAuditTopics := []string{
		"audit.events.current-account.v1",
		"audit.events.financial-accounting.v1",
		"audit.events.position-keeping.v1",
		"audit.events.party.v1",
		"audit.events.payment-order.v1",
		"audit.events.tenant.v1",
	}

	config := &Config{
		KafkaBootstrapServers:   env.GetEnvOrDefault("KAFKA_BOOTSTRAP_SERVERS", ""),
		ConsumerGroupID:         env.GetEnvOrDefault("CONSUMER_GROUP_ID", "utilization-metering-consumer"),
		AuditTopics:             defaultAuditTopics, // Use default topics
		PositionKeepingEndpoint: env.GetEnvOrDefault("POSITION_KEEPING_ENDPOINT", ""),
		TenantZeroID:            env.GetEnvOrDefault("TENANT_ZERO_ID", ""),
		TenantAccountMapping:    env.GetEnvOrDefault("TENANT_ACCOUNT_MAPPING", ""),
		HTTPPort:                env.GetEnvOrDefault("HTTP_PORT", "8080"),
		EnableMDSOutput:         env.GetEnvAsBool("ENABLE_MDS_OUTPUT", true),
		MDSServiceAddr:          env.GetEnvOrDefault("MDS_SERVICE_ADDR", ""),
		MDSAggregationWindow:    env.GetEnvAsDuration("MDS_AGGREGATION_WINDOW", 1*time.Hour),
		MDSFlushInterval:        env.GetEnvAsDuration("MDS_FLUSH_INTERVAL", 5*time.Minute),
	}

	// Validate required configuration
	if config.KafkaBootstrapServers == "" {
		return nil, ErrKafkaBootstrapServersRequired
	}
	// Note: ConsumerGroupID validation removed - it has a default value so can never be empty
	if config.PositionKeepingEndpoint == "" {
		return nil, ErrPositionKeepingEndpointRequired
	}
	if config.TenantZeroID == "" {
		return nil, ErrTenantZeroIDRequired
	}

	// Validate tenant zero ID format using proper UUID parsing
	if _, err := uuid.Parse(config.TenantZeroID); err != nil {
		return nil, fmt.Errorf("invalid TENANT_ZERO_ID UUID format: %w", err)
	}

	return config, nil
}
