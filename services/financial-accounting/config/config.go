// Package config provides configuration types for the financial-accounting service.
package config

import (
	"time"

	"github.com/meridianhub/meridian/shared/platform/env"
)

// IdempotencyCleanupConfig holds configuration for the idempotency cleanup worker.
type IdempotencyCleanupConfig struct {
	// Enabled controls whether the cleanup worker runs.
	// Default: true
	Enabled bool

	// StaleThreshold is the duration after which a PENDING key is considered stale.
	// Keys in PENDING state longer than this will be marked as FAILED.
	// Default: 15 minutes
	StaleThreshold time.Duration

	// RunInterval is how often the cleanup worker polls for stale keys.
	// Default: 5 minutes
	RunInterval time.Duration

	// BatchSize is the maximum number of stale keys to process per iteration.
	// Default: 100
	BatchSize int

	// KeyPattern is the Redis key pattern to scan for idempotency keys.
	// Default: "idempotency:result:*"
	KeyPattern string
}

// DefaultIdempotencyCleanupConfig returns the default cleanup configuration.
func DefaultIdempotencyCleanupConfig() IdempotencyCleanupConfig {
	return IdempotencyCleanupConfig{
		Enabled:        true,
		StaleThreshold: 15 * time.Minute,
		RunInterval:    5 * time.Minute,
		BatchSize:      100,
		KeyPattern:     "idempotency:result:*",
	}
}

// LoadIdempotencyCleanupConfig loads cleanup configuration from environment variables.
//
// Environment variables:
//   - IDEMPOTENCY_CLEANUP_ENABLED: Enable/disable the cleanup worker (default: true)
//   - IDEMPOTENCY_CLEANUP_STALE_THRESHOLD: Duration before PENDING keys are stale (default: 15m)
//   - IDEMPOTENCY_CLEANUP_RUN_INTERVAL: How often to run cleanup (default: 5m)
//   - IDEMPOTENCY_CLEANUP_BATCH_SIZE: Max keys per cleanup iteration (default: 100)
//   - IDEMPOTENCY_CLEANUP_KEY_PATTERN: Redis key pattern to scan (default: idempotency:result:*)
func LoadIdempotencyCleanupConfig() IdempotencyCleanupConfig {
	defaults := DefaultIdempotencyCleanupConfig()

	return IdempotencyCleanupConfig{
		Enabled:        env.GetEnvAsBool("IDEMPOTENCY_CLEANUP_ENABLED", defaults.Enabled),
		StaleThreshold: env.GetEnvAsDuration("IDEMPOTENCY_CLEANUP_STALE_THRESHOLD", defaults.StaleThreshold),
		RunInterval:    env.GetEnvAsDuration("IDEMPOTENCY_CLEANUP_RUN_INTERVAL", defaults.RunInterval),
		BatchSize:      env.GetEnvAsInt("IDEMPOTENCY_CLEANUP_BATCH_SIZE", defaults.BatchSize),
		KeyPattern:     env.GetEnvOrDefault("IDEMPOTENCY_CLEANUP_KEY_PATTERN", defaults.KeyPattern),
	}
}
