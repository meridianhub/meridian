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
type Config struct {
	BootstrapServers string
	ClientID         string
}

// NewConfigFromEnv creates Kafka configuration from environment variables.
// Expects KAFKA_BOOTSTRAP_SERVERS and optionally KAFKA_CLIENT_ID.
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
func DefaultConfig() Config {
	return Config{
		BootstrapServers: "localhost:9092",
		ClientID:         DefaultClientID,
	}
}
