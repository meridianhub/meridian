package kafka

import (
	"os"
	"testing"
)

func TestNewConfigFromEnv(t *testing.T) {
	// Save original env vars
	origBootstrap := os.Getenv("KAFKA_BOOTSTRAP_SERVERS")
	origClientID := os.Getenv("KAFKA_CLIENT_ID")
	defer func() {
		if origBootstrap != "" {
			_ = os.Setenv("KAFKA_BOOTSTRAP_SERVERS", origBootstrap)
		} else {
			_ = os.Unsetenv("KAFKA_BOOTSTRAP_SERVERS")
		}
		if origClientID != "" {
			_ = os.Setenv("KAFKA_CLIENT_ID", origClientID)
		} else {
			_ = os.Unsetenv("KAFKA_CLIENT_ID")
		}
	}()

	t.Run("valid config with both env vars", func(t *testing.T) {
		_ = os.Setenv("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092")
		_ = os.Setenv("KAFKA_CLIENT_ID", "my-service")

		config, err := NewConfigFromEnv()
		if err != nil {
			t.Errorf("NewConfigFromEnv() error = %v, want nil", err)
		}
		if config.BootstrapServers != "kafka:9092" {
			t.Errorf("BootstrapServers = %v, want kafka:9092", config.BootstrapServers)
		}
		if config.ClientID != "my-service" {
			t.Errorf("ClientID = %v, want my-service", config.ClientID)
		}
	})

	t.Run("valid config with default client ID", func(t *testing.T) {
		_ = os.Setenv("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092")
		_ = os.Unsetenv("KAFKA_CLIENT_ID")

		config, err := NewConfigFromEnv()
		if err != nil {
			t.Errorf("NewConfigFromEnv() error = %v, want nil", err)
		}
		if config.ClientID != "meridian-service" {
			t.Errorf("ClientID = %v, want meridian-service", config.ClientID)
		}
	})

	t.Run("missing bootstrap servers", func(t *testing.T) {
		_ = os.Unsetenv("KAFKA_BOOTSTRAP_SERVERS")

		_, err := NewConfigFromEnv()
		if err == nil {
			t.Error("NewConfigFromEnv() error = nil, want error")
		}
	})
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.BootstrapServers != "localhost:9092" {
		t.Errorf("BootstrapServers = %v, want localhost:9092", config.BootstrapServers)
	}
	if config.ClientID != "meridian-service" {
		t.Errorf("ClientID = %v, want meridian-service", config.ClientID)
	}
}
