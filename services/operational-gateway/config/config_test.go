package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestLoadConfig_Defaults verifies all default values are applied when no env vars are set.
func TestLoadConfig_Defaults(t *testing.T) {
	clearEnv(t)

	cfg := LoadConfig()

	assert.NotEmpty(t, cfg.GRPCPort, "GRPCPort should have a default")
	assert.Empty(t, cfg.DatabaseURL, "DatabaseURL default should be empty")
	assert.Equal(t, "info", cfg.LogLevel)
	assert.Equal(t, 50, cfg.DispatchWorker.BatchSize)
	assert.Equal(t, 1*time.Second, cfg.DispatchWorker.PollInterval)
	assert.Equal(t, 30*time.Second, cfg.ExpiryWorker.ScanInterval)
	assert.Equal(t, 100, cfg.ExpiryWorker.BatchSize)
}

// TestLoadConfig_GRPCPort verifies GRPC_PORT env var is read.
func TestLoadConfig_GRPCPort(t *testing.T) {
	t.Setenv("GRPC_PORT", "9090")

	cfg := LoadConfig()

	assert.Equal(t, "9090", cfg.GRPCPort)
}

// TestLoadConfig_DatabaseURL verifies DATABASE_URL env var is read.
func TestLoadConfig_DatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost/mydb")

	cfg := LoadConfig()

	assert.Equal(t, "postgres://user:pass@localhost/mydb", cfg.DatabaseURL)
}

// TestLoadConfig_LogLevel verifies LOG_LEVEL env var is read.
func TestLoadConfig_LogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "debug")

	cfg := LoadConfig()

	assert.Equal(t, "debug", cfg.LogLevel)
}

// TestLoadConfig_DispatchWorkerBatchSize verifies DISPATCH_WORKER_BATCH_SIZE env var is read.
func TestLoadConfig_DispatchWorkerBatchSize(t *testing.T) {
	t.Setenv("DISPATCH_WORKER_BATCH_SIZE", "25")

	cfg := LoadConfig()

	assert.Equal(t, 25, cfg.DispatchWorker.BatchSize)
}

// TestLoadConfig_DispatchWorkerBatchSize_InvalidFallsBackToDefault verifies invalid int uses default.
func TestLoadConfig_DispatchWorkerBatchSize_InvalidFallsBackToDefault(t *testing.T) {
	t.Setenv("DISPATCH_WORKER_BATCH_SIZE", "not-a-number")

	cfg := LoadConfig()

	assert.Equal(t, 50, cfg.DispatchWorker.BatchSize)
}

// TestLoadConfig_DispatchWorkerPollInterval verifies DISPATCH_WORKER_POLL_INTERVAL env var is read.
func TestLoadConfig_DispatchWorkerPollInterval(t *testing.T) {
	t.Setenv("DISPATCH_WORKER_POLL_INTERVAL", "500ms")

	cfg := LoadConfig()

	assert.Equal(t, 500*time.Millisecond, cfg.DispatchWorker.PollInterval)
}

// TestLoadConfig_ExpiryWorkerScanInterval verifies EXPIRY_WORKER_SCAN_INTERVAL env var is read.
func TestLoadConfig_ExpiryWorkerScanInterval(t *testing.T) {
	t.Setenv("EXPIRY_WORKER_SCAN_INTERVAL", "1m")

	cfg := LoadConfig()

	assert.Equal(t, 1*time.Minute, cfg.ExpiryWorker.ScanInterval)
}

// TestLoadConfig_ExpiryWorkerBatchSize verifies EXPIRY_WORKER_BATCH_SIZE env var is read.
func TestLoadConfig_ExpiryWorkerBatchSize(t *testing.T) {
	t.Setenv("EXPIRY_WORKER_BATCH_SIZE", "200")

	cfg := LoadConfig()

	assert.Equal(t, 200, cfg.ExpiryWorker.BatchSize)
}

// TestLoadConfig_AllEnvVarsSet verifies all env vars can be set together.
func TestLoadConfig_AllEnvVarsSet(t *testing.T) {
	t.Setenv("GRPC_PORT", "8080")
	t.Setenv("DATABASE_URL", "postgres://host/db")
	t.Setenv("LOG_LEVEL", "warn")
	t.Setenv("DISPATCH_WORKER_BATCH_SIZE", "10")
	t.Setenv("DISPATCH_WORKER_POLL_INTERVAL", "2s")
	t.Setenv("EXPIRY_WORKER_SCAN_INTERVAL", "5m")
	t.Setenv("EXPIRY_WORKER_BATCH_SIZE", "50")

	cfg := LoadConfig()

	assert.Equal(t, "8080", cfg.GRPCPort)
	assert.Equal(t, "postgres://host/db", cfg.DatabaseURL)
	assert.Equal(t, "warn", cfg.LogLevel)
	assert.Equal(t, 10, cfg.DispatchWorker.BatchSize)
	assert.Equal(t, 2*time.Second, cfg.DispatchWorker.PollInterval)
	assert.Equal(t, 5*time.Minute, cfg.ExpiryWorker.ScanInterval)
	assert.Equal(t, 50, cfg.ExpiryWorker.BatchSize)
}

// clearEnv unsets all config-related environment variables for a test.
func clearEnv(t *testing.T) {
	t.Helper()
	vars := []string{
		"GRPC_PORT",
		"DATABASE_URL",
		"LOG_LEVEL",
		"DISPATCH_WORKER_BATCH_SIZE",
		"DISPATCH_WORKER_POLL_INTERVAL",
		"EXPIRY_WORKER_SCAN_INTERVAL",
		"EXPIRY_WORKER_BATCH_SIZE",
	}
	for _, v := range vars {
		t.Setenv(v, "")
	}
}
