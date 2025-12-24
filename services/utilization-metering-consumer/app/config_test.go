package app

import (
	"errors"
	"os"
	"testing"
)

func TestLoadConfig_Success(t *testing.T) {
	// Set required environment variables
	envVars := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS":   "kafka:9092",
		"CONSUMER_GROUP_ID":         "utilization-metering-consumer",
		"POSITION_KEEPING_ENDPOINT": "position-keeping:50051",
		"TENANT_ZERO_ID":            "00000000-0000-0000-0000-000000000000",
	}

	// Backup and set environment variables
	backup := make(map[string]string)
	for key, value := range envVars {
		backup[key] = os.Getenv(key)
		os.Setenv(key, value)
	}
	defer func() {
		// Restore original environment
		for key, value := range backup {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	// Load configuration
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}

	// Verify configuration values
	if config.KafkaBootstrapServers != "kafka:9092" {
		t.Errorf("Expected KafkaBootstrapServers to be 'kafka:9092', got '%s'", config.KafkaBootstrapServers)
	}
	if config.ConsumerGroupID != "utilization-metering-consumer" {
		t.Errorf("Expected ConsumerGroupID to be 'utilization-metering-consumer', got '%s'", config.ConsumerGroupID)
	}
	if config.PositionKeepingEndpoint != "position-keeping:50051" {
		t.Errorf("Expected PositionKeepingEndpoint to be 'position-keeping:50051', got '%s'", config.PositionKeepingEndpoint)
	}
	if config.TenantZeroID != "00000000-0000-0000-0000-000000000000" {
		t.Errorf("Expected TenantZeroID to be '00000000-0000-0000-0000-000000000000', got '%s'", config.TenantZeroID)
	}
	// Verify default audit topics (6 services)
	expectedTopics := []string{
		"current-account.audit.events",
		"financial-accounting.audit.events",
		"position-keeping.audit.events",
		"party.audit.events",
		"payment-order.audit.events",
		"tenant.audit.events",
	}
	if len(config.AuditTopics) != len(expectedTopics) {
		t.Errorf("Expected %d audit topics, got %d", len(expectedTopics), len(config.AuditTopics))
	}
	for i, expected := range expectedTopics {
		if i >= len(config.AuditTopics) || config.AuditTopics[i] != expected {
			t.Errorf("Expected AuditTopics[%d] to be '%s', got '%s'", i, expected, config.AuditTopics[i])
		}
	}
	if config.HTTPPort != "8080" {
		t.Errorf("Expected HTTPPort to be '8080' (default), got '%s'", config.HTTPPort)
	}
}

func TestLoadConfig_MissingKafkaBootstrapServers(t *testing.T) {
	// Clear all required environment variables
	backup := make(map[string]string)
	envKeys := []string{"KAFKA_BOOTSTRAP_SERVERS", "CONSUMER_GROUP_ID", "POSITION_KEEPING_ENDPOINT", "TENANT_ZERO_ID"}
	for _, key := range envKeys {
		backup[key] = os.Getenv(key)
		os.Unsetenv(key)
	}
	defer func() {
		for key, value := range backup {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	// Load configuration
	_, err := LoadConfig()
	if !errors.Is(err, ErrKafkaBootstrapServersRequired) {
		t.Errorf("Expected ErrKafkaBootstrapServersRequired, got %v", err)
	}
}

func TestLoadConfig_MissingPositionKeepingEndpoint_First(t *testing.T) {
	// Set only some required variables (removed ConsumerGroupID check since it has a default)
	envVars := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS": "kafka:9092",
	}

	backup := make(map[string]string)
	for key, value := range envVars {
		backup[key] = os.Getenv(key)
		os.Setenv(key, value)
	}
	// Clear other required vars
	otherKeys := []string{"CONSUMER_GROUP_ID", "POSITION_KEEPING_ENDPOINT", "TENANT_ZERO_ID"}
	for _, key := range otherKeys {
		if _, exists := backup[key]; !exists {
			backup[key] = os.Getenv(key)
		}
		os.Unsetenv(key)
	}
	defer func() {
		for key, value := range backup {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	// Load configuration - should fail on missing POSITION_KEEPING_ENDPOINT
	_, err := LoadConfig()
	if !errors.Is(err, ErrPositionKeepingEndpointRequired) {
		t.Errorf("Expected ErrPositionKeepingEndpointRequired, got %v", err)
	}
}

func TestLoadConfig_MissingPositionKeepingEndpoint(t *testing.T) {
	// Set only some required variables
	envVars := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS": "kafka:9092",
		"CONSUMER_GROUP_ID":       "test-group",
	}

	backup := make(map[string]string)
	for key, value := range envVars {
		backup[key] = os.Getenv(key)
		os.Setenv(key, value)
	}
	// Clear other required vars
	otherKeys := []string{"POSITION_KEEPING_ENDPOINT", "TENANT_ZERO_ID"}
	for _, key := range otherKeys {
		if _, exists := backup[key]; !exists {
			backup[key] = os.Getenv(key)
		}
		os.Unsetenv(key)
	}
	defer func() {
		for key, value := range backup {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	// Load configuration
	_, err := LoadConfig()
	if !errors.Is(err, ErrPositionKeepingEndpointRequired) {
		t.Errorf("Expected ErrPositionKeepingEndpointRequired, got %v", err)
	}
}

func TestLoadConfig_MissingTenantZeroID(t *testing.T) {
	// Set only some required variables
	envVars := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS":   "kafka:9092",
		"CONSUMER_GROUP_ID":         "test-group",
		"POSITION_KEEPING_ENDPOINT": "position-keeping:50051",
	}

	backup := make(map[string]string)
	for key, value := range envVars {
		backup[key] = os.Getenv(key)
		os.Setenv(key, value)
	}
	// Clear TENANT_ZERO_ID
	if val := os.Getenv("TENANT_ZERO_ID"); val != "" {
		backup["TENANT_ZERO_ID"] = val
	}
	os.Unsetenv("TENANT_ZERO_ID")
	defer func() {
		for key, value := range backup {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	// Load configuration
	_, err := LoadConfig()
	if !errors.Is(err, ErrTenantZeroIDRequired) {
		t.Errorf("Expected ErrTenantZeroIDRequired, got %v", err)
	}
}

func TestLoadConfig_InvalidTenantZeroID(t *testing.T) {
	// Set all required variables with invalid UUID
	envVars := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS":   "kafka:9092",
		"CONSUMER_GROUP_ID":         "test-group",
		"POSITION_KEEPING_ENDPOINT": "position-keeping:50051",
		"TENANT_ZERO_ID":            "invalid-uuid",
	}

	backup := make(map[string]string)
	for key, value := range envVars {
		backup[key] = os.Getenv(key)
		os.Setenv(key, value)
	}
	defer func() {
		for key, value := range backup {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	// Load configuration
	_, err := LoadConfig()
	if err == nil {
		t.Error("Expected error for invalid TENANT_ZERO_ID UUID, got nil")
	}
}

func TestLoadConfig_DefaultConsumerGroupID(t *testing.T) {
	// Set required environment variables WITHOUT consumer group ID to test default
	envVars := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS":   "kafka:9092",
		"POSITION_KEEPING_ENDPOINT": "position-keeping:50051",
		"TENANT_ZERO_ID":            "00000000-0000-0000-0000-000000000000",
	}

	backup := make(map[string]string)
	for key, value := range envVars {
		backup[key] = os.Getenv(key)
		os.Setenv(key, value)
	}
	// Ensure CONSUMER_GROUP_ID is not set
	if val := os.Getenv("CONSUMER_GROUP_ID"); val != "" {
		backup["CONSUMER_GROUP_ID"] = val
	}
	os.Unsetenv("CONSUMER_GROUP_ID")

	defer func() {
		for key, value := range backup {
			if value == "" {
				os.Unsetenv(key)
			} else {
				os.Setenv(key, value)
			}
		}
	}()

	// Load configuration
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}

	if config.ConsumerGroupID != "utilization-metering-consumer" {
		t.Errorf("Expected ConsumerGroupID to be 'utilization-metering-consumer' (default), got '%s'", config.ConsumerGroupID)
	}
}
