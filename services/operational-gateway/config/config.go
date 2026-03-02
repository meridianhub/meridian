// Package config provides configuration loading for the operational-gateway service.
package config

import (
	"strconv"
	"time"

	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/meridianhub/meridian/shared/platform/ports"
)

// Config holds the operational-gateway service configuration.
type Config struct {
	// GRPCPort is the gRPC listen port.
	GRPCPort string

	// DatabaseURL is the CockroachDB/PostgreSQL connection string.
	DatabaseURL string

	// LogLevel controls the log verbosity (debug, info, warn, error).
	LogLevel string

	// DispatchWorker configures the background dispatch worker.
	DispatchWorker DispatchWorkerConfig

	// ExpiryWorker configures the background expiry worker.
	ExpiryWorker ExpiryWorkerConfig
}

// DispatchWorkerConfig configures the dispatch worker.
type DispatchWorkerConfig struct {
	// BatchSize is the maximum number of instructions to claim per poll cycle.
	BatchSize int
	// PollInterval is the duration between successive poll cycles.
	PollInterval time.Duration
}

// ExpiryWorkerConfig configures the expiry worker.
type ExpiryWorkerConfig struct {
	// ScanInterval is the duration between successive expiry scan cycles.
	ScanInterval time.Duration
	// BatchSize is the maximum number of expired instructions to process per scan.
	BatchSize int
}

// LoadConfig loads configuration from environment variables with sensible defaults.
func LoadConfig() Config {
	return Config{
		GRPCPort:    env.GetEnvOrDefault("GRPC_PORT", strconv.Itoa(ports.OperationalGateway)),
		DatabaseURL: env.GetEnvOrDefault("DATABASE_URL", ""),
		LogLevel:    env.GetEnvOrDefault("LOG_LEVEL", "info"),
		DispatchWorker: DispatchWorkerConfig{
			BatchSize:    env.GetEnvAsInt("DISPATCH_WORKER_BATCH_SIZE", 50),
			PollInterval: env.GetEnvAsDuration("DISPATCH_WORKER_POLL_INTERVAL", 1*time.Second),
		},
		ExpiryWorker: ExpiryWorkerConfig{
			ScanInterval: env.GetEnvAsDuration("EXPIRY_WORKER_SCAN_INTERVAL", 30*time.Second),
			BatchSize:    env.GetEnvAsInt("EXPIRY_WORKER_BATCH_SIZE", 100),
		},
	}
}
