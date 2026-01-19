// Package config provides configuration loading for the Market Information service.
// BIAN Service Domain: Market Information Management
package config

import (
	"time"

	"github.com/meridianhub/meridian/shared/platform/env"
)

// ECBConfig holds configuration for the ECB daily FX rates adapter.
type ECBConfig struct {
	// Enabled controls whether the ECB worker runs.
	// Default: false (opt-in)
	Enabled bool

	// Endpoint is the ECB SDMX Web Service URL.
	// Default: uses the ECB client's default endpoint
	Endpoint string

	// SourceCode is the data source code registered in Market Information.
	// Default: "ECB"
	SourceCode string

	// DatasetCode is the dataset code prefix for ECB FX rates.
	// Default: "ECB_FX"
	DatasetCode string

	// Interval is how often to fetch new rates from ECB.
	// Default: 24 hours
	Interval time.Duration

	// Timeout is the HTTP request timeout for ECB API calls.
	// Default: 30 seconds
	Timeout time.Duration

	// MaxRetries is the maximum number of retry attempts for failed operations.
	// Default: 3
	MaxRetries int
}

// DefaultECBConfig returns the default ECB configuration.
func DefaultECBConfig() ECBConfig {
	return ECBConfig{
		Enabled:     false,
		Endpoint:    "", // Uses ECB client's default
		SourceCode:  "ECB",
		DatasetCode: "ECB_FX",
		Interval:    24 * time.Hour,
		Timeout:     30 * time.Second,
		MaxRetries:  3,
	}
}

// LoadECBConfig loads ECB configuration from environment variables.
//
// Environment variables:
//   - ECB_ENABLED: Enable/disable the ECB worker (default: false)
//   - ECB_ENDPOINT: ECB SDMX Web Service URL (default: uses client default)
//   - ECB_SOURCE_CODE: Data source code in Market Information (default: ECB)
//   - ECB_DATASET_CODE: Dataset code prefix for FX rates (default: ECB_FX)
//   - ECB_INTERVAL: How often to fetch rates (default: 24h)
//   - ECB_TIMEOUT: HTTP request timeout (default: 30s)
//   - ECB_MAX_RETRIES: Maximum retry attempts (default: 3)
func LoadECBConfig() ECBConfig {
	defaults := DefaultECBConfig()

	return ECBConfig{
		Enabled:     env.GetEnvAsBool("ECB_ENABLED", defaults.Enabled),
		Endpoint:    env.GetEnvOrDefault("ECB_ENDPOINT", defaults.Endpoint),
		SourceCode:  env.GetEnvOrDefault("ECB_SOURCE_CODE", defaults.SourceCode),
		DatasetCode: env.GetEnvOrDefault("ECB_DATASET_CODE", defaults.DatasetCode),
		Interval:    env.GetEnvAsDuration("ECB_INTERVAL", defaults.Interval),
		Timeout:     env.GetEnvAsDuration("ECB_TIMEOUT", defaults.Timeout),
		MaxRetries:  env.GetEnvAsInt("ECB_MAX_RETRIES", defaults.MaxRetries),
	}
}

// Config holds all configuration for the Market Information service.
type Config struct {
	// ECB holds the ECB adapter configuration.
	ECB ECBConfig
}

// LoadConfig loads all service configuration from environment variables.
func LoadConfig() Config {
	return Config{
		ECB: LoadECBConfig(),
	}
}
