package kafka

import (
	"errors"
	"os"
)

const (
	// DefaultClientID is the default Kafka client identifier.
	DefaultClientID = "meridian-service"
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
