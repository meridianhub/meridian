package kafka

import (
	"os"
	"testing"
)

func TestNewConfigFromEnv(t *testing.T) {
	tests := []struct {
		name                 string
		bootstrapServers     string
		clientID             string
		wantBootstrapServers string
		wantClientID         string
		wantErr              bool
	}{
		{
			name:                 "valid config with both env vars",
			bootstrapServers:     "kafka:9092",
			clientID:             "my-service",
			wantBootstrapServers: "kafka:9092",
			wantClientID:         "my-service",
			wantErr:              false,
		},
		{
			name:                 "valid config with default client ID",
			bootstrapServers:     "kafka:9092",
			clientID:             "",
			wantBootstrapServers: "kafka:9092",
			wantClientID:         "meridian-service",
			wantErr:              false,
		},
		{
			name:                 "missing bootstrap servers",
			bootstrapServers:     "",
			clientID:             "my-service",
			wantBootstrapServers: "",
			wantClientID:         "",
			wantErr:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original env vars
			origBootstrap := os.Getenv("KAFKA_BOOTSTRAP_SERVERS")
			origClientID := os.Getenv("KAFKA_CLIENT_ID")
			defer func() {
				if origBootstrap != "" {
					os.Setenv("KAFKA_BOOTSTRAP_SERVERS", origBootstrap)
				} else {
					os.Unsetenv("KAFKA_BOOTSTRAP_SERVERS")
				}
				if origClientID != "" {
					os.Setenv("KAFKA_CLIENT_ID", origClientID)
				} else {
					os.Unsetenv("KAFKA_CLIENT_ID")
				}
			}()

			// Set test env vars
			if tt.bootstrapServers != "" {
				os.Setenv("KAFKA_BOOTSTRAP_SERVERS", tt.bootstrapServers)
			} else {
				os.Unsetenv("KAFKA_BOOTSTRAP_SERVERS")
			}
			if tt.clientID != "" {
				os.Setenv("KAFKA_CLIENT_ID", tt.clientID)
			} else {
				os.Unsetenv("KAFKA_CLIENT_ID")
			}

			config, err := NewConfigFromEnv()
			if (err != nil) != tt.wantErr {
				t.Errorf("NewConfigFromEnv() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if config.BootstrapServers != tt.wantBootstrapServers {
					t.Errorf("BootstrapServers = %v, want %v", config.BootstrapServers, tt.wantBootstrapServers)
				}
				if config.ClientID != tt.wantClientID {
					t.Errorf("ClientID = %v, want %v", config.ClientID, tt.wantClientID)
				}
			}
		})
	}
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
