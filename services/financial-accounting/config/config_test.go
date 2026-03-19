package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultIdempotencyCleanupConfig(t *testing.T) {
	cfg := DefaultIdempotencyCleanupConfig()

	assert.True(t, cfg.Enabled)
	assert.Equal(t, 15*time.Minute, cfg.StaleThreshold)
	assert.Equal(t, 5*time.Minute, cfg.RunInterval)
	assert.Equal(t, 100, cfg.BatchSize)
	assert.Equal(t, "idempotency:result:*", cfg.KeyPattern)
}

func TestLoadIdempotencyCleanupConfig_Defaults(t *testing.T) {
	// Ensure env vars are unset so we get pure defaults
	t.Setenv("IDEMPOTENCY_CLEANUP_ENABLED", "")
	t.Setenv("IDEMPOTENCY_CLEANUP_STALE_THRESHOLD", "")
	t.Setenv("IDEMPOTENCY_CLEANUP_RUN_INTERVAL", "")
	t.Setenv("IDEMPOTENCY_CLEANUP_BATCH_SIZE", "")
	t.Setenv("IDEMPOTENCY_CLEANUP_KEY_PATTERN", "")

	cfg := LoadIdempotencyCleanupConfig()

	defaults := DefaultIdempotencyCleanupConfig()
	assert.Equal(t, defaults.Enabled, cfg.Enabled)
	assert.Equal(t, defaults.StaleThreshold, cfg.StaleThreshold)
	assert.Equal(t, defaults.RunInterval, cfg.RunInterval)
	assert.Equal(t, defaults.BatchSize, cfg.BatchSize)
	assert.Equal(t, defaults.KeyPattern, cfg.KeyPattern)
}

func TestLoadIdempotencyCleanupConfig_CustomValues(t *testing.T) {
	t.Setenv("IDEMPOTENCY_CLEANUP_ENABLED", "false")
	t.Setenv("IDEMPOTENCY_CLEANUP_STALE_THRESHOLD", "30m")
	t.Setenv("IDEMPOTENCY_CLEANUP_RUN_INTERVAL", "10m")
	t.Setenv("IDEMPOTENCY_CLEANUP_BATCH_SIZE", "250")
	t.Setenv("IDEMPOTENCY_CLEANUP_KEY_PATTERN", "custom:pattern:*")

	cfg := LoadIdempotencyCleanupConfig()

	assert.False(t, cfg.Enabled)
	assert.Equal(t, 30*time.Minute, cfg.StaleThreshold)
	assert.Equal(t, 10*time.Minute, cfg.RunInterval)
	assert.Equal(t, 250, cfg.BatchSize)
	assert.Equal(t, "custom:pattern:*", cfg.KeyPattern)
}

func TestLoadIdempotencyCleanupConfig_PartialOverride(t *testing.T) {
	// Only override some values; others should remain defaults
	t.Setenv("IDEMPOTENCY_CLEANUP_BATCH_SIZE", "50")

	cfg := LoadIdempotencyCleanupConfig()

	defaults := DefaultIdempotencyCleanupConfig()
	assert.Equal(t, defaults.Enabled, cfg.Enabled)
	assert.Equal(t, defaults.StaleThreshold, cfg.StaleThreshold)
	assert.Equal(t, defaults.RunInterval, cfg.RunInterval)
	assert.Equal(t, 50, cfg.BatchSize)
	assert.Equal(t, defaults.KeyPattern, cfg.KeyPattern)
}

func TestLoadIdempotencyCleanupConfig_InvalidBoolFallsBackToDefault(t *testing.T) {
	t.Setenv("IDEMPOTENCY_CLEANUP_ENABLED", "not-a-bool")

	cfg := LoadIdempotencyCleanupConfig()

	// GetEnvAsBool should return the default when parsing fails
	defaults := DefaultIdempotencyCleanupConfig()
	assert.Equal(t, defaults.Enabled, cfg.Enabled)
}
