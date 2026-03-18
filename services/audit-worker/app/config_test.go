package app

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name        string
		envVars     map[string]string
		wantErr     bool
		expectedErr error
		validate    func(*testing.T, *Config)
	}{
		{
			name: "valid configuration with all required fields",
			envVars: map[string]string{
				"SERVICE_NAME":            "current-account",
				"DATABASE_URL":            "postgres://user:pass@localhost:5432/db",
				"KAFKA_BOOTSTRAP_SERVERS": "kafka:9092",
				"AUDIT_TOPIC":             "audit.events.current-account.v1",
			},
			wantErr: false,
			validate: func(t *testing.T, c *Config) {
				if c.Service.Name != "current-account" {
					t.Errorf("Service.Name = %s, want current-account", c.Service.Name)
				}
				if c.Database.URL != "postgres://user:pass@localhost:5432/db" {
					t.Errorf("Database.URL = %s, want postgres://user:pass@localhost:5432/db", c.Database.URL)
				}
				if c.Kafka.BootstrapServers != "kafka:9092" {
					t.Errorf("Kafka.BootstrapServers = %s, want kafka:9092", c.Kafka.BootstrapServers)
				}
				if c.Kafka.Topic != "audit.events.current-account.v1" {
					t.Errorf("Kafka.Topic = %s, want audit.events.current-account.v1", c.Kafka.Topic)
				}
			},
		},
		{
			name: "missing SERVICE_NAME",
			envVars: map[string]string{
				"DATABASE_URL":            "postgres://user:pass@localhost:5432/db",
				"KAFKA_BOOTSTRAP_SERVERS": "kafka:9092",
				"AUDIT_TOPIC":             "audit.events.v1",
			},
			wantErr:     true,
			expectedErr: ErrEmptyServiceName,
		},
		{
			name: "missing DATABASE_URL",
			envVars: map[string]string{
				"SERVICE_NAME":            "current-account",
				"KAFKA_BOOTSTRAP_SERVERS": "kafka:9092",
				"AUDIT_TOPIC":             "audit.events.v1",
			},
			wantErr:     true,
			expectedErr: ErrEmptyDatabaseURL,
		},
		{
			name: "missing KAFKA_BOOTSTRAP_SERVERS",
			envVars: map[string]string{
				"SERVICE_NAME": "current-account",
				"DATABASE_URL": "postgres://user:pass@localhost:5432/db",
				"AUDIT_TOPIC":  "audit.events.v1",
			},
			wantErr:     true,
			expectedErr: ErrEmptyBootstrapServers,
		},
		{
			name: "missing AUDIT_TOPIC",
			envVars: map[string]string{
				"SERVICE_NAME":            "current-account",
				"DATABASE_URL":            "postgres://user:pass@localhost:5432/db",
				"KAFKA_BOOTSTRAP_SERVERS": "kafka:9092",
			},
			wantErr:     true,
			expectedErr: ErrEmptyTopic,
		},
		{
			name: "custom port and timeouts",
			envVars: map[string]string{
				"SERVICE_NAME":              "financial-accounting",
				"DATABASE_URL":              "postgres://user:pass@localhost:5432/db",
				"KAFKA_BOOTSTRAP_SERVERS":   "kafka:9092",
				"AUDIT_TOPIC":               "audit.events.financial-accounting.v1",
				"PORT":                      "9090",
				"GRACEFUL_SHUTDOWN_TIMEOUT": "60s",
				"KAFKA_HANDLER_TIMEOUT":     "45s",
			},
			wantErr: false,
			validate: func(t *testing.T, c *Config) {
				if c.Service.Port != "9090" {
					t.Errorf("Service.Port = %s, want 9090", c.Service.Port)
				}
				if c.Service.GracefulShutdownTimeout != 60*time.Second {
					t.Errorf("Service.GracefulShutdownTimeout = %v, want 60s", c.Service.GracefulShutdownTimeout)
				}
				if c.Kafka.HandlerTimeout != 45*time.Second {
					t.Errorf("Kafka.HandlerTimeout = %v, want 45s", c.Kafka.HandlerTimeout)
				}
			},
		},
		{
			name: "custom database connection pool settings",
			envVars: map[string]string{
				"SERVICE_NAME":            "position-keeping",
				"DATABASE_URL":            "postgres://user:pass@localhost:5432/db",
				"KAFKA_BOOTSTRAP_SERVERS": "kafka:9092",
				"AUDIT_TOPIC":             "audit.events.position-keeping.v1",
				"DB_MAX_OPEN_CONNS":       "50",
				"DB_MAX_IDLE_CONNS":       "10",
				"DB_CONN_MAX_LIFETIME":    "10m",
				"DB_CONN_MAX_IDLE_TIME":   "5m",
			},
			wantErr: false,
			validate: func(t *testing.T, c *Config) {
				if c.Database.MaxOpenConns != 50 {
					t.Errorf("Database.MaxOpenConns = %d, want 50", c.Database.MaxOpenConns)
				}
				if c.Database.MaxIdleConns != 10 {
					t.Errorf("Database.MaxIdleConns = %d, want 10", c.Database.MaxIdleConns)
				}
				if c.Database.ConnMaxLifetime != 10*time.Minute {
					t.Errorf("Database.ConnMaxLifetime = %v, want 10m", c.Database.ConnMaxLifetime)
				}
				if c.Database.ConnMaxIdleTime != 5*time.Minute {
					t.Errorf("Database.ConnMaxIdleTime = %v, want 5m", c.Database.ConnMaxIdleTime)
				}
			},
		},
		{
			name: "custom kafka settings",
			envVars: map[string]string{
				"SERVICE_NAME":            "payment-order",
				"DATABASE_URL":            "postgres://user:pass@localhost:5432/db",
				"KAFKA_BOOTSTRAP_SERVERS": "kafka1:9092,kafka2:9092",
				"AUDIT_TOPIC":             "audit.events.payment-order.v1",
				"KAFKA_GROUP_ID":          "payment-order-audit-consumer",
				"KAFKA_CLIENT_ID":         "payment-order-consumer-1",
				"KAFKA_MAX_RETRIES":       "5",
			},
			wantErr: false,
			validate: func(t *testing.T, c *Config) {
				if c.Kafka.BootstrapServers != "kafka1:9092,kafka2:9092" {
					t.Errorf("Kafka.BootstrapServers = %s, want kafka1:9092,kafka2:9092", c.Kafka.BootstrapServers)
				}
				if c.Kafka.GroupID != "payment-order-audit-consumer" {
					t.Errorf("Kafka.GroupID = %s, want payment-order-audit-consumer", c.Kafka.GroupID)
				}
				if c.Kafka.ClientID != "payment-order-consumer-1" {
					t.Errorf("Kafka.ClientID = %s, want payment-order-consumer-1", c.Kafka.ClientID)
				}
				if c.Kafka.MaxRetries != 5 {
					t.Errorf("Kafka.MaxRetries = %d, want 5", c.Kafka.MaxRetries)
				}
			},
		},
		{
			name: "defaults are applied when optional fields omitted",
			envVars: map[string]string{
				"SERVICE_NAME":            "tenant",
				"DATABASE_URL":            "postgres://user:pass@localhost:5432/db",
				"KAFKA_BOOTSTRAP_SERVERS": "kafka:9092",
				"AUDIT_TOPIC":             "audit.events.tenant.v1",
			},
			wantErr: false,
			validate: func(t *testing.T, c *Config) {
				// Service defaults
				if c.Service.Port != "8080" {
					t.Errorf("Service.Port = %s, want 8080 (default)", c.Service.Port)
				}
				if c.Service.GracefulShutdownTimeout != 30*time.Second {
					t.Errorf("Service.GracefulShutdownTimeout = %v, want 30s (default)", c.Service.GracefulShutdownTimeout)
				}

				// Database defaults
				if c.Database.MaxOpenConns != 25 {
					t.Errorf("Database.MaxOpenConns = %d, want 25 (default)", c.Database.MaxOpenConns)
				}
				if c.Database.MaxIdleConns != 5 {
					t.Errorf("Database.MaxIdleConns = %d, want 5 (default)", c.Database.MaxIdleConns)
				}

				// Kafka defaults
				if c.Kafka.GroupID != "audit-consumer-group" {
					t.Errorf("Kafka.GroupID = %s, want audit-consumer-group (default)", c.Kafka.GroupID)
				}
				if c.Kafka.ClientID != "audit-consumer" {
					t.Errorf("Kafka.ClientID = %s, want audit-consumer (default)", c.Kafka.ClientID)
				}
				if c.Kafka.HandlerTimeout != 30*time.Second {
					t.Errorf("Kafka.HandlerTimeout = %v, want 30s (default)", c.Kafka.HandlerTimeout)
				}
				if c.Kafka.MaxRetries != 3 {
					t.Errorf("Kafka.MaxRetries = %d, want 3 (default)", c.Kafka.MaxRetries)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear environment before test
			os.Clearenv()

			// Set test environment variables
			for k, v := range tt.envVars {
				os.Setenv(k, v)
			}

			// Load configuration
			config, err := LoadConfig()

			// Check error expectation
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadConfig() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			// Check specific error if expected (use errors.Is for wrapped errors)
			if tt.expectedErr != nil && !errors.Is(err, tt.expectedErr) {
				t.Errorf("LoadConfig() error = %v, expectedErr %v", err, tt.expectedErr)
				return
			}

			// Run validation function if provided
			if tt.validate != nil && config != nil {
				tt.validate(t, config)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
		wantErr     bool
		expectedErr error
	}{
		{
			name: "valid configuration",
			config: &Config{
				Service: ServiceConfig{
					Name: "current-account",
					Port: "8080",
				},
				Database: DatabaseConfig{
					URL:          "postgres://localhost/db",
					MaxOpenConns: 10,
					MaxIdleConns: 2,
				},
				Kafka: KafkaConfig{
					BootstrapServers: "kafka:9092",
					Topic:            "audit.events.v1",
					GroupID:          "group-1",
				},
			},
			wantErr: false,
		},
		{
			name: "invalid max open connections (zero)",
			config: &Config{
				Service: ServiceConfig{
					Name: "current-account",
					Port: "8080",
				},
				Database: DatabaseConfig{
					URL:          "postgres://localhost/db",
					MaxOpenConns: 0, // Invalid
					MaxIdleConns: 2,
				},
				Kafka: KafkaConfig{
					BootstrapServers: "kafka:9092",
					Topic:            "audit.events.v1",
					GroupID:          "group-1",
				},
			},
			wantErr:     true,
			expectedErr: ErrInvalidMaxOpenConns,
		},
		{
			name: "invalid max idle connections (negative)",
			config: &Config{
				Service: ServiceConfig{
					Name: "current-account",
					Port: "8080",
				},
				Database: DatabaseConfig{
					URL:          "postgres://localhost/db",
					MaxOpenConns: 10,
					MaxIdleConns: -1, // Invalid
				},
				Kafka: KafkaConfig{
					BootstrapServers: "kafka:9092",
					Topic:            "audit.events.v1",
					GroupID:          "group-1",
				},
			},
			wantErr:     true,
			expectedErr: ErrInvalidMaxIdleConns,
		},
		{
			name: "empty service port",
			config: &Config{
				Service: ServiceConfig{
					Name: "current-account",
					Port: "", // Invalid
				},
				Database: DatabaseConfig{
					URL:          "postgres://localhost/db",
					MaxOpenConns: 10,
					MaxIdleConns: 2,
				},
				Kafka: KafkaConfig{
					BootstrapServers: "kafka:9092",
					Topic:            "audit.events.v1",
					GroupID:          "group-1",
				},
			},
			wantErr:     true,
			expectedErr: ErrEmptyPort,
		},
		{
			name: "empty Kafka GroupID",
			config: &Config{
				Service: ServiceConfig{
					Name: "current-account",
					Port: "8080",
				},
				Database: DatabaseConfig{
					URL:          "postgres://localhost/db",
					MaxOpenConns: 10,
					MaxIdleConns: 2,
				},
				Kafka: KafkaConfig{
					BootstrapServers: "kafka:9092",
					Topic:            "audit.events.v1",
					GroupID:          "", // Invalid
				},
			},
			wantErr:     true,
			expectedErr: ErrEmptyGroupID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()

			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.expectedErr != nil && !errors.Is(err, tt.expectedErr) {
				t.Errorf("Config.Validate() error = %v, expectedErr %v", err, tt.expectedErr)
			}
		})
	}
}

func TestGetEnvAsInt_InvalidFormat(t *testing.T) {
	os.Clearenv()
	os.Setenv("DB_MAX_OPEN_CONNS", "not_a_number")
	os.Setenv("SERVICE_NAME", "test")
	os.Setenv("DATABASE_URL", "postgres://localhost/db")
	os.Setenv("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092")
	os.Setenv("AUDIT_TOPIC", "audit.events.v1")

	config, err := LoadConfig()
	require.NoError(t, err)
	// Should fall back to default (25)
	assert.Equal(t, 25, config.Database.MaxOpenConns)
}

func TestGetEnvAsInt_OutOfInt32Range(t *testing.T) {
	os.Clearenv()
	os.Setenv("DB_MAX_OPEN_CONNS", "9999999999") // > MaxInt32
	os.Setenv("SERVICE_NAME", "test")
	os.Setenv("DATABASE_URL", "postgres://localhost/db")
	os.Setenv("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092")
	os.Setenv("AUDIT_TOPIC", "audit.events.v1")

	config, err := LoadConfig()
	require.NoError(t, err)
	// Should fall back to default (25)
	assert.Equal(t, 25, config.Database.MaxOpenConns)
}

func TestGetEnvAsDuration_InvalidFormat(t *testing.T) {
	os.Clearenv()
	os.Setenv("KAFKA_HANDLER_TIMEOUT", "not_a_duration")
	os.Setenv("SERVICE_NAME", "test")
	os.Setenv("DATABASE_URL", "postgres://localhost/db")
	os.Setenv("KAFKA_BOOTSTRAP_SERVERS", "kafka:9092")
	os.Setenv("AUDIT_TOPIC", "audit.events.v1")

	config, err := LoadConfig()
	require.NoError(t, err)
	// Should fall back to default (30s)
	assert.Equal(t, 30*time.Second, config.Kafka.HandlerTimeout)
}
