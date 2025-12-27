package app

import (
	"os"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/env"
)

func TestLoadConfig_Defaults(t *testing.T) {
	// Clear environment
	clearEnv(t)
	// DATABASE_URL is required (no default to avoid hardcoded credentials)
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/testdb")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil", err)
	}

	// Verify server defaults
	if config.Server.Port != "50053" {
		t.Errorf("Server.Port = %s, want 50053", config.Server.Port)
	}
	if config.Server.GracefulShutdownTimeout != 30*time.Second {
		t.Errorf("Server.GracefulShutdownTimeout = %v, want 30s", config.Server.GracefulShutdownTimeout)
	}

	// Verify database defaults
	if config.Database.MaxOpenConns != 25 {
		t.Errorf("Database.MaxOpenConns = %d, want 25", config.Database.MaxOpenConns)
	}
	if config.Database.MaxIdleConns != 5 {
		t.Errorf("Database.MaxIdleConns = %d, want 5", config.Database.MaxIdleConns)
	}

	// Verify Kafka defaults
	if !config.Kafka.Enabled {
		t.Error("Kafka.Enabled = false, want true")
	}
	if len(config.Kafka.Brokers) != 1 || config.Kafka.Brokers[0] != "kafka:9092" {
		t.Errorf("Kafka.Brokers = %v, want [kafka:9092]", config.Kafka.Brokers)
	}

	// Verify Redis defaults
	if config.Redis.Enabled {
		t.Error("Redis.Enabled = true, want false")
	}

	// Verify observability defaults
	if config.Observability.ServiceName != "position-keeping-service" {
		t.Errorf("Observability.ServiceName = %s, want position-keeping-service", config.Observability.ServiceName)
	}
	if config.Observability.SamplingRate != 1.0 {
		t.Errorf("Observability.SamplingRate = %f, want 1.0", config.Observability.SamplingRate)
	}
}

func TestLoadConfig_CustomValues(t *testing.T) {
	// Clear and set custom environment
	clearEnv(t)
	t.Setenv("GRPC_PORT", "8080")
	t.Setenv("GRACEFUL_SHUTDOWN_TIMEOUT", "60s")
	t.Setenv("DATABASE_URL", "postgres://custom:pass@localhost:5432/db")
	t.Setenv("DB_MAX_OPEN_CONNS", "50")
	t.Setenv("DB_MAX_IDLE_CONNS", "10")
	t.Setenv("DB_CONN_MAX_LIFETIME", "10m")
	t.Setenv("DB_CONN_MAX_IDLE_TIME", "15m")
	t.Setenv("DB_HEALTH_CHECK_INTERVAL", "60s")
	t.Setenv("KAFKA_BROKERS", "broker1:9092,broker2:9092")
	t.Setenv("KAFKA_TOPIC", "custom-topic")
	t.Setenv("KAFKA_ENABLED", "false")
	t.Setenv("KAFKA_PRODUCER_TIMEOUT", "30s")
	t.Setenv("REDIS_ADDRESS", "localhost:6379")
	t.Setenv("REDIS_PASSWORD", "secret")
	t.Setenv("REDIS_DB", "1")
	t.Setenv("REDIS_ENABLED", "true")
	t.Setenv("REDIS_POOL_SIZE", "20")
	t.Setenv("REDIS_CONN_MAX_IDLE_TIME", "10m")
	t.Setenv("SERVICE_NAME", "custom-service")
	t.Setenv("SERVICE_VERSION", "1.0.0")
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("OTLP_ENDPOINT", "localhost:4317")
	t.Setenv("SAMPLING_RATE", "0.1")
	t.Setenv("LOG_LEVEL", "debug")
	t.Setenv("METRICS_ENABLED", "false")
	t.Setenv("METRICS_PORT", "9091")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want nil", err)
	}

	// Verify server config
	if config.Server.Port != "8080" {
		t.Errorf("Server.Port = %s, want 8080", config.Server.Port)
	}
	if config.Server.GracefulShutdownTimeout != 60*time.Second {
		t.Errorf("Server.GracefulShutdownTimeout = %v, want 60s", config.Server.GracefulShutdownTimeout)
	}

	// Verify database config
	if config.Database.URL != "postgres://custom:pass@localhost:5432/db" {
		t.Errorf("Database.URL = %s, want custom URL", config.Database.URL)
	}
	if config.Database.MaxOpenConns != 50 {
		t.Errorf("Database.MaxOpenConns = %d, want 50", config.Database.MaxOpenConns)
	}
	if config.Database.MaxIdleConns != 10 {
		t.Errorf("Database.MaxIdleConns = %d, want 10", config.Database.MaxIdleConns)
	}
	if config.Database.ConnMaxLifetime != 10*time.Minute {
		t.Errorf("Database.ConnMaxLifetime = %v, want 10m", config.Database.ConnMaxLifetime)
	}
	if config.Database.ConnMaxIdleTime != 15*time.Minute {
		t.Errorf("Database.ConnMaxIdleTime = %v, want 15m", config.Database.ConnMaxIdleTime)
	}
	if config.Database.HealthCheckInterval != 60*time.Second {
		t.Errorf("Database.HealthCheckInterval = %v, want 60s", config.Database.HealthCheckInterval)
	}

	// Verify Kafka config
	if config.Kafka.Enabled {
		t.Error("Kafka.Enabled = true, want false")
	}
	expectedBrokers := []string{"broker1:9092", "broker2:9092"}
	if len(config.Kafka.Brokers) != len(expectedBrokers) {
		t.Errorf("Kafka.Brokers length = %d, want %d", len(config.Kafka.Brokers), len(expectedBrokers))
	}
	for i, broker := range expectedBrokers {
		if config.Kafka.Brokers[i] != broker {
			t.Errorf("Kafka.Brokers[%d] = %s, want %s", i, config.Kafka.Brokers[i], broker)
		}
	}
	if config.Kafka.Topic != "custom-topic" {
		t.Errorf("Kafka.Topic = %s, want custom-topic", config.Kafka.Topic)
	}
	if config.Kafka.ProducerTimeout != 30*time.Second {
		t.Errorf("Kafka.ProducerTimeout = %v, want 30s", config.Kafka.ProducerTimeout)
	}

	// Verify Redis config
	if !config.Redis.Enabled {
		t.Error("Redis.Enabled = false, want true")
	}
	if config.Redis.Address != "localhost:6379" {
		t.Errorf("Redis.Address = %s, want localhost:6379", config.Redis.Address)
	}
	if config.Redis.Password != "secret" {
		t.Errorf("Redis.Password = %s, want secret", config.Redis.Password)
	}
	if config.Redis.DB != 1 {
		t.Errorf("Redis.DB = %d, want 1", config.Redis.DB)
	}
	if config.Redis.PoolSize != 20 {
		t.Errorf("Redis.PoolSize = %d, want 20", config.Redis.PoolSize)
	}
	if config.Redis.ConnMaxIdleTime != 10*time.Minute {
		t.Errorf("Redis.ConnMaxIdleTime = %v, want 10m", config.Redis.ConnMaxIdleTime)
	}

	// Verify observability config
	if config.Observability.ServiceName != "custom-service" {
		t.Errorf("Observability.ServiceName = %s, want custom-service", config.Observability.ServiceName)
	}
	if config.Observability.ServiceVersion != "1.0.0" {
		t.Errorf("Observability.ServiceVersion = %s, want 1.0.0", config.Observability.ServiceVersion)
	}
	if config.Observability.Environment != "production" {
		t.Errorf("Observability.Environment = %s, want production", config.Observability.Environment)
	}
	if config.Observability.OTLPEndpoint != "localhost:4317" {
		t.Errorf("Observability.OTLPEndpoint = %s, want localhost:4317", config.Observability.OTLPEndpoint)
	}
	if config.Observability.SamplingRate != 0.1 {
		t.Errorf("Observability.SamplingRate = %f, want 0.1", config.Observability.SamplingRate)
	}
	if config.Observability.LogLevel != "debug" {
		t.Errorf("Observability.LogLevel = %s, want debug", config.Observability.LogLevel)
	}
	if config.Observability.MetricsEnabled {
		t.Error("Observability.MetricsEnabled = true, want false")
	}
	if config.Observability.MetricsPort != "9091" {
		t.Errorf("Observability.MetricsPort = %s, want 9091", config.Observability.MetricsPort)
	}
}

func TestValidate_Success(t *testing.T) {
	config := &Config{
		Server: ServerConfig{
			Port:                    "50053",
			GracefulShutdownTimeout: 30 * time.Second,
		},
		Database: DatabaseConfig{
			URL:          "postgres://localhost:5432/db",
			MaxOpenConns: 10,
			MaxIdleConns: 5,
		},
		Kafka: KafkaConfig{
			Enabled: true,
			Brokers: []string{"localhost:9092"},
		},
		Observability: ObservabilityConfig{
			SamplingRate: 0.5,
		},
	}

	if err := config.Validate(); err != nil {
		t.Errorf("Validate() error = %v, want nil", err)
	}
}

func TestValidate_EmptyPort(t *testing.T) {
	config := &Config{
		Server: ServerConfig{
			Port: "",
		},
		Database: DatabaseConfig{
			URL:          "postgres://localhost:5432/db",
			MaxOpenConns: 10,
		},
		Observability: ObservabilityConfig{
			SamplingRate: 0.5,
		},
	}

	err := config.Validate()
	if err == nil {
		t.Error("Validate() error = nil, want error for empty port")
	}
}

func TestValidate_EmptyDatabaseURL(t *testing.T) {
	config := &Config{
		Server: ServerConfig{
			Port: "50053",
		},
		Database: DatabaseConfig{
			URL:          "",
			MaxOpenConns: 10,
		},
		Observability: ObservabilityConfig{
			SamplingRate: 0.5,
		},
	}

	err := config.Validate()
	if err == nil {
		t.Error("Validate() error = nil, want error for empty database URL")
	}
}

func TestLoadConfig_WhitespaceOnlyDatabaseURL(t *testing.T) {
	// Test that whitespace-only DATABASE_URL is trimmed to empty and caught by validation
	clearEnv(t)
	t.Setenv("DATABASE_URL", "   \t\n   ")

	_, err := LoadConfig()
	if err == nil {
		t.Error("LoadConfig() error = nil, want error for whitespace-only DATABASE_URL")
	}
	// Should get validation error for empty database URL after trimming
}

func TestValidate_InvalidMaxOpenConns(t *testing.T) {
	config := &Config{
		Server: ServerConfig{
			Port: "50053",
		},
		Database: DatabaseConfig{
			URL:          "postgres://localhost:5432/db",
			MaxOpenConns: 0,
		},
		Observability: ObservabilityConfig{
			SamplingRate: 0.5,
		},
	}

	err := config.Validate()
	if err == nil {
		t.Error("Validate() error = nil, want error for MaxOpenConns < 1")
	}
}

func TestValidate_NegativeMaxIdleConns(t *testing.T) {
	config := &Config{
		Server: ServerConfig{
			Port: "50053",
		},
		Database: DatabaseConfig{
			URL:          "postgres://localhost:5432/db",
			MaxOpenConns: 10,
			MaxIdleConns: -1,
		},
		Observability: ObservabilityConfig{
			SamplingRate: 0.5,
		},
	}

	err := config.Validate()
	if err == nil {
		t.Error("Validate() error = nil, want error for negative MaxIdleConns")
	}
}

func TestValidate_KafkaEnabledWithoutBrokers(t *testing.T) {
	config := &Config{
		Server: ServerConfig{
			Port: "50053",
		},
		Database: DatabaseConfig{
			URL:          "postgres://localhost:5432/db",
			MaxOpenConns: 10,
		},
		Kafka: KafkaConfig{
			Enabled: true,
			Brokers: []string{},
		},
		Observability: ObservabilityConfig{
			SamplingRate: 0.5,
		},
	}

	err := config.Validate()
	if err == nil {
		t.Error("Validate() error = nil, want error for enabled Kafka without brokers")
	}
}

func TestValidate_InvalidSamplingRateTooLow(t *testing.T) {
	config := &Config{
		Server: ServerConfig{
			Port: "50053",
		},
		Database: DatabaseConfig{
			URL:          "postgres://localhost:5432/db",
			MaxOpenConns: 10,
		},
		Observability: ObservabilityConfig{
			SamplingRate: -0.1,
		},
	}

	err := config.Validate()
	if err == nil {
		t.Error("Validate() error = nil, want error for sampling rate < 0")
	}
}

func TestValidate_MaxOpenConnsOverflow(t *testing.T) {
	config := &Config{
		Server: ServerConfig{
			Port: "50053",
		},
		Database: DatabaseConfig{
			URL:          "postgres://localhost:5432/db",
			MaxOpenConns: 2147483648, // Exceeds int32 max
		},
		Observability: ObservabilityConfig{
			SamplingRate: 0.5,
		},
	}

	err := config.Validate()
	if err == nil {
		t.Error("Validate() error = nil, want error for MaxOpenConns overflow")
	}
}

func TestValidate_MaxIdleConnsOverflow(t *testing.T) {
	config := &Config{
		Server: ServerConfig{
			Port: "50053",
		},
		Database: DatabaseConfig{
			URL:          "postgres://localhost:5432/db",
			MaxOpenConns: 10,
			MaxIdleConns: 2147483648, // Exceeds int32 max
		},
		Observability: ObservabilityConfig{
			SamplingRate: 0.5,
		},
	}

	err := config.Validate()
	if err == nil {
		t.Error("Validate() error = nil, want error for MaxIdleConns overflow")
	}
}

func TestValidate_InvalidSamplingRateTooHigh(t *testing.T) {
	config := &Config{
		Server: ServerConfig{
			Port: "50053",
		},
		Database: DatabaseConfig{
			URL:          "postgres://localhost:5432/db",
			MaxOpenConns: 10,
		},
		Observability: ObservabilityConfig{
			SamplingRate: 1.1,
		},
	}

	err := config.Validate()
	if err == nil {
		t.Error("Validate() error = nil, want error for sampling rate > 1")
	}
}

func TestGetEnvAsSlice_SingleValue(t *testing.T) {
	t.Setenv("TEST_SLICE", "value1")
	result := env.GetEnvAsSlice("TEST_SLICE", []string{"default"})
	if len(result) != 1 || result[0] != "value1" {
		t.Errorf("env.GetEnvAsSlice() = %v, want [value1]", result)
	}
}

func TestGetEnvAsSlice_MultipleValues(t *testing.T) {
	t.Setenv("TEST_SLICE", "value1,value2,value3")
	result := env.GetEnvAsSlice("TEST_SLICE", []string{"default"})
	expected := []string{"value1", "value2", "value3"}
	if len(result) != len(expected) {
		t.Errorf("env.GetEnvAsSlice() length = %d, want %d", len(result), len(expected))
	}
	for i, v := range expected {
		if result[i] != v {
			t.Errorf("env.GetEnvAsSlice()[%d] = %s, want %s", i, result[i], v)
		}
	}
}

func TestGetEnvAsSlice_EmptyValue(t *testing.T) {
	t.Setenv("TEST_SLICE", "")
	defaultVal := []string{"default1", "default2"}
	result := env.GetEnvAsSlice("TEST_SLICE", defaultVal)
	if len(result) != len(defaultVal) {
		t.Errorf("env.GetEnvAsSlice() length = %d, want %d", len(result), len(defaultVal))
	}
}

func TestGetEnvAsBool_True(t *testing.T) {
	tests := []string{"true", "True", "TRUE", "1", "t", "T"}
	for _, val := range tests {
		t.Run(val, func(t *testing.T) {
			t.Setenv("TEST_BOOL", val)
			result := env.GetEnvAsBool("TEST_BOOL", false)
			if !result {
				t.Errorf("env.GetEnvAsBool(%s) = false, want true", val)
			}
		})
	}
}

func TestGetEnvAsBool_False(t *testing.T) {
	tests := []string{"false", "False", "FALSE", "0", "f", "F"}
	for _, val := range tests {
		t.Run(val, func(t *testing.T) {
			t.Setenv("TEST_BOOL", val)
			result := env.GetEnvAsBool("TEST_BOOL", true)
			if result {
				t.Errorf("env.GetEnvAsBool(%s) = true, want false", val)
			}
		})
	}
}

func TestGetEnvAsFloat_Valid(t *testing.T) {
	t.Setenv("TEST_FLOAT", "0.75")
	result := env.GetEnvAsFloat("TEST_FLOAT", 0.0)
	if result != 0.75 {
		t.Errorf("env.GetEnvAsFloat() = %f, want 0.75", result)
	}
}

func TestGetEnvAsFloat_Invalid(t *testing.T) {
	t.Setenv("TEST_FLOAT", "invalid")
	result := env.GetEnvAsFloat("TEST_FLOAT", 0.5)
	if result != 0.5 {
		t.Errorf("env.GetEnvAsFloat() = %f, want 0.5 (default)", result)
	}
}

func TestGetEnvAsSlice_WithWhitespace(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		expected []string
	}{
		{
			name:     "spaces around values",
			value:    " kafka1:9092 , kafka2:9092 , kafka3:9092 ",
			expected: []string{"kafka1:9092", "kafka2:9092", "kafka3:9092"},
		},
		{
			name:     "tabs and spaces",
			value:    "value1\t,\tvalue2\t,\tvalue3",
			expected: []string{"value1", "value2", "value3"},
		},
		{
			name:     "no whitespace",
			value:    "value1,value2,value3",
			expected: []string{"value1", "value2", "value3"},
		},
		{
			name:     "empty values filtered",
			value:    "value1, , value3",
			expected: []string{"value1", "value3"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("TEST_SLICE", tt.value)
			result := env.GetEnvAsSlice("TEST_SLICE", []string{"default"})
			if len(result) != len(tt.expected) {
				t.Errorf("env.GetEnvAsSlice() length = %d, want %d", len(result), len(tt.expected))
				return
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("env.GetEnvAsSlice()[%d] = %s, want %s", i, v, tt.expected[i])
				}
			}
		})
	}
}

// clearEnv clears environment variables used in tests
func clearEnv(t *testing.T) {
	t.Helper()
	envVars := []string{
		"GRPC_PORT", "GRACEFUL_SHUTDOWN_TIMEOUT",
		"DATABASE_URL", "DB_MAX_OPEN_CONNS", "DB_MAX_IDLE_CONNS",
		"DB_CONN_MAX_LIFETIME", "DB_CONN_MAX_IDLE_TIME", "DB_HEALTH_CHECK_INTERVAL",
		"KAFKA_BROKERS", "KAFKA_TOPIC", "KAFKA_ENABLED", "KAFKA_PRODUCER_TIMEOUT",
		"REDIS_ADDRESS", "REDIS_PASSWORD", "REDIS_DB", "REDIS_ENABLED",
		"REDIS_POOL_SIZE", "REDIS_CONN_MAX_IDLE_TIME",
		"SERVICE_NAME", "SERVICE_VERSION", "ENVIRONMENT", "OTLP_ENDPOINT",
		"SAMPLING_RATE", "LOG_LEVEL", "METRICS_ENABLED", "METRICS_PORT",
	}
	for _, key := range envVars {
		_ = os.Unsetenv(key)
	}
}
