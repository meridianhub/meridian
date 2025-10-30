package kafka

import (
	"fmt"
	"os"
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
		return Config{}, fmt.Errorf("KAFKA_BOOTSTRAP_SERVERS environment variable is required")
	}

	clientID := os.Getenv("KAFKA_CLIENT_ID")
	if clientID == "" {
		clientID = "meridian-service"
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
		ClientID:         "meridian-service",
	}
}
