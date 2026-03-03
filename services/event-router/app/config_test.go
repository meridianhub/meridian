package app

import (
	"errors"
	"os"
	"testing"
	"time"
)

func TestLoadConfig_Success(t *testing.T) {
	// Set required environment variables
	envVars := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS":   "kafka:9092",
		"CONSUMER_GROUP_ID":         "event-router",
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
	if config.ConsumerGroupID != "event-router" {
		t.Errorf("Expected ConsumerGroupID to be 'event-router', got '%s'", config.ConsumerGroupID)
	}
	if config.PositionKeepingEndpoint != "position-keeping:50051" {
		t.Errorf("Expected PositionKeepingEndpoint to be 'position-keeping:50051', got '%s'", config.PositionKeepingEndpoint)
	}
	if config.TenantZeroID != "00000000-0000-0000-0000-000000000000" {
		t.Errorf("Expected TenantZeroID to be '00000000-0000-0000-0000-000000000000', got '%s'", config.TenantZeroID)
	}
	// Verify default audit topics (6 services, service-name.event-name.v1 convention)
	expectedTopics := []string{
		"audit.events.current-account.v1",
		"audit.events.financial-accounting.v1",
		"audit.events.position-keeping.v1",
		"audit.events.party.v1",
		"audit.events.payment-order.v1",
		"audit.events.tenant.v1",
	}
	if len(config.AuditTopics) != len(expectedTopics) {
		t.Errorf("Expected %d audit topics, got %d", len(expectedTopics), len(config.AuditTopics))
	}
	// Verify each topic with proper bounds checking
	for i, expected := range expectedTopics {
		if i >= len(config.AuditTopics) {
			t.Errorf("Expected AuditTopics[%d] to be '%s', but index is out of bounds (length: %d)", i, expected, len(config.AuditTopics))
			continue
		}
		if config.AuditTopics[i] != expected {
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

	if config.ConsumerGroupID != "event-router" {
		t.Errorf("Expected ConsumerGroupID to be 'event-router' (default), got '%s'", config.ConsumerGroupID)
	}
}

func TestLoadConfig_MDSDefaults(t *testing.T) {
	// Set required environment variables
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
	// Clear MDS-related vars to test defaults
	mdsKeys := []string{"ENABLE_MDS_OUTPUT", "MDS_SERVICE_ADDR", "MDS_AGGREGATION_WINDOW", "MDS_FLUSH_INTERVAL"}
	for _, key := range mdsKeys {
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

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}

	if !config.EnableMDSOutput {
		t.Error("Expected EnableMDSOutput to default to true")
	}
	if config.MDSServiceAddr != "" {
		t.Errorf("Expected MDSServiceAddr to be empty by default, got '%s'", config.MDSServiceAddr)
	}
	if config.MDSAggregationWindow != 1*time.Hour {
		t.Errorf("Expected MDSAggregationWindow to default to 1h, got %v", config.MDSAggregationWindow)
	}
	if config.MDSFlushInterval != 5*time.Minute {
		t.Errorf("Expected MDSFlushInterval to default to 5m, got %v", config.MDSFlushInterval)
	}
}

func TestLoadConfig_MDSCustomValues(t *testing.T) {
	envVars := map[string]string{
		"KAFKA_BOOTSTRAP_SERVERS":   "kafka:9092",
		"POSITION_KEEPING_ENDPOINT": "position-keeping:50051",
		"TENANT_ZERO_ID":            "00000000-0000-0000-0000-000000000000",
		"ENABLE_MDS_OUTPUT":         "false",
		"MDS_SERVICE_ADDR":          "market-information:50058",
		"MDS_AGGREGATION_WINDOW":    "30m",
		"MDS_FLUSH_INTERVAL":        "2m",
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

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() failed: %v", err)
	}

	if config.EnableMDSOutput {
		t.Error("Expected EnableMDSOutput to be false")
	}
	if config.MDSServiceAddr != "market-information:50058" {
		t.Errorf("Expected MDSServiceAddr to be 'market-information:50058', got '%s'", config.MDSServiceAddr)
	}
	if config.MDSAggregationWindow != 30*time.Minute {
		t.Errorf("Expected MDSAggregationWindow to be 30m, got %v", config.MDSAggregationWindow)
	}
	if config.MDSFlushInterval != 2*time.Minute {
		t.Errorf("Expected MDSFlushInterval to be 2m, got %v", config.MDSFlushInterval)
	}
}
