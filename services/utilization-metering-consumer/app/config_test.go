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
	if config.AuditTopic != "audit.events" {
		t.Errorf("Expected AuditTopic to be 'audit.events' (default), got '%s'", config.AuditTopic)
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

func TestLoadConfig_MissingConsumerGroupID(t *testing.T) {
	// Set only some required variables
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

	// Load configuration
	_, err := LoadConfig()
	if !errors.Is(err, ErrConsumerGroupIDRequired) {
		t.Errorf("Expected ErrConsumerGroupIDRequired, got %v", err)
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

func TestLoadConfig_CustomAuditTopic(t *testing.T) {
	// Set required environment variables with custom audit topic
	envVars := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS":   "kafka:9092",
		"CONSUMER_GROUP_ID":         "test-group",
		"POSITION_KEEPING_ENDPOINT": "position-keeping:50051",
		"TENANT_ZERO_ID":            "00000000-0000-0000-0000-000000000000",
		"AUDIT_TOPIC":               "custom.audit.topic",
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
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}

	if config.AuditTopic != "custom.audit.topic" {
		t.Errorf("Expected AuditTopic to be 'custom.audit.topic', got '%s'", config.AuditTopic)
	}
}
