package kafka

import (
	"errors"
	"os"

	"github.com/meridianhub/meridian/shared/platform/events/topics"
)

const (
	// DefaultClientID is the default Kafka client identifier.
	DefaultClientID = "meridian-service"

	// AuditEventsTopic is the Kafka topic for audit events.
	// References the centralized topic registry in shared/platform/events/topics.
	AuditEventsTopic = topics.AuditEventsV1
	// AuditEventsDLQTopic is the dead letter queue for failed audit events.
	// References the centralized topic registry in shared/platform/events/topics.
	AuditEventsDLQTopic = topics.AuditEventsDLQV1

	// DeprecatedAuditEventsTopic is the old topic name for migration backwards compatibility.
	DeprecatedAuditEventsTopic = "audit.events"
	// DeprecatedAuditEventsDLQTopic is the old DLQ topic name.
	DeprecatedAuditEventsDLQTopic = "audit.events.dlq"

	// AuditConsumerGroup is the consumer group for audit event processing.
	AuditConsumerGroup = "audit-consumer-group"

	// DefaultAuditRetentionDays is the default retention period for audit topics.
	// Compliance requirements typically mandate 7 years, but Kafka retention
	// is set to 30 days as audit_log provides permanent storage.
	DefaultAuditRetentionDays = 30
)

var (
	// ErrEmptyBootstrapServers is returned when bootstrap servers configuration is empty.
	ErrEmptyBootstrapServers = errors.New("bootstrap servers cannot be empty")
	// ErrMissingBootstrapServers is returned when KAFKA_BOOTSTRAP_SERVERS env var is not set.
	ErrMissingBootstrapServers = errors.New("KAFKA_BOOTSTRAP_SERVERS environment variable is required")
)

// Config contains common Kafka connection configuration.
// This is used for services that need both producer and consumer with shared settings.
type Config struct {
	// BootstrapServers is the comma-separated list of Kafka broker addresses.
	BootstrapServers string
	// ClientID identifies the application for logging and metrics.
	ClientID string
}

// NewConfigFromEnv creates Kafka configuration from environment variables.
// This is the recommended way to configure Kafka connections in containerized environments.
//
// Environment variables:
// - KAFKA_BOOTSTRAP_SERVERS: Required. Comma-separated list of broker addresses (e.g., "kafka:9092")
// - KAFKA_CLIENT_ID: Optional. Defaults to "meridian-service" if not set
//
// Returns an error if KAFKA_BOOTSTRAP_SERVERS is not set.
func NewConfigFromEnv() (Config, error) {
	bootstrapServers := os.Getenv("KAFKA_BOOTSTRAP_SERVERS")
	if bootstrapServers == "" {
		return Config{}, ErrMissingBootstrapServers
	}

	clientID := os.Getenv("KAFKA_CLIENT_ID")
	if clientID == "" {
		clientID = DefaultClientID
	}

	return Config{
		BootstrapServers: bootstrapServers,
		ClientID:         clientID,
	}, nil
}

// DefaultConfig returns default Kafka configuration for local development.
// This connects to localhost:9092 with client ID "meridian-service".
// Use this for testing or local development when Kafka is running in Docker Compose or locally.
func DefaultConfig() Config {
	return Config{
		BootstrapServers: "localhost:9092",
		ClientID:         DefaultClientID,
	}
}

// AuditTopicConfig contains configuration for audit-related Kafka topics.
type AuditTopicConfig struct {
	// EventsTopic is the topic for publishing audit events.
	EventsTopic string
	// DLQTopic is the dead letter queue for failed audit events.
	DLQTopic string
	// ConsumerGroup is the consumer group ID for audit processing.
	ConsumerGroup string
	// RetentionDays is the number of days to retain messages in the topic.
	RetentionDays int
	// Partitions is the number of partitions for the audit topic.
	Partitions int
	// ReplicationFactor is the replication factor for the audit topic.
	ReplicationFactor int
}

// DefaultAuditTopicConfig returns the default configuration for audit topics.
func DefaultAuditTopicConfig() AuditTopicConfig {
	return AuditTopicConfig{
		EventsTopic:       AuditEventsTopic,
		DLQTopic:          AuditEventsDLQTopic,
		ConsumerGroup:     AuditConsumerGroup,
		RetentionDays:     DefaultAuditRetentionDays,
		Partitions:        6,
		ReplicationFactor: 3,
	}
}

// AuditTopicConfigFromEnv creates audit topic configuration from environment variables.
// Falls back to defaults for any unset variables.
//
// Environment variables:
// - AUDIT_KAFKA_TOPIC: Topic name for audit events (default: "audit.events.v1")
// - AUDIT_KAFKA_DLQ_TOPIC: Dead letter queue topic (default: "audit.events.v1.dlq")
// - AUDIT_KAFKA_CONSUMER_GROUP: Consumer group ID (default: "audit-consumer-group")
func AuditTopicConfigFromEnv() AuditTopicConfig {
	config := DefaultAuditTopicConfig()

	if topic := os.Getenv("AUDIT_KAFKA_TOPIC"); topic != "" {
		config.EventsTopic = topic
	}
	if dlqTopic := os.Getenv("AUDIT_KAFKA_DLQ_TOPIC"); dlqTopic != "" {
		config.DLQTopic = dlqTopic
	}
	if group := os.Getenv("AUDIT_KAFKA_CONSUMER_GROUP"); group != "" {
		config.ConsumerGroup = group
	}

	return config
}
