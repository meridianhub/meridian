// Package config provides configuration loading for the Forecasting service.
package config

import (
	"github.com/meridianhub/meridian/shared/platform/env"
)

// Config holds all configuration for the Forecasting service.
type Config struct {
	// DatabaseURL is the CockroachDB connection string.
	DatabaseURL string
}

// LoadConfig loads all service configuration from environment variables.
func LoadConfig() Config {
	return Config{
		DatabaseURL: env.GetEnvOrDefault("DATABASE_URL",
			"postgres://meridian_user@localhost:26257/meridian_forecasting?sslmode=disable"),
	}
}
