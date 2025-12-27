// Package gateway provides the multi-tenant API gateway for routing requests
// to backend services based on tenant identification from subdomain or header.
package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds the configuration for the gateway service.
type Config struct {
	// Port is the HTTP port to listen on (default 8080).
	Port int

	// BaseDomain is the base domain for subdomain-based tenant identification
	// (e.g., "api.meridianhub.cloud").
	BaseDomain string

	// LocalDevMode allows using X-Tenant-Slug header for tenant identification
	// in development environments (default false).
	LocalDevMode bool

	// DatabaseURL is the connection string for tenant lookups.
	DatabaseURL string

	// RedisURL is the optional Redis URL for caching (use in-memory if empty).
	RedisURL string

	// Backends is the list of backend routes for request proxying.
	Backends []BackendRoute
}

// BackendRoute defines a mapping from a URL prefix to a backend service.
type BackendRoute struct {
	// Prefix is the URL path prefix to match (e.g., "/v1/party").
	Prefix string `json:"prefix"`

	// Target is the backend service address (e.g., "party-service:50051").
	Target string `json:"target"`
}

// Configuration errors.
var (
	// ErrBaseDomainRequired is returned when BASE_DOMAIN is not set.
	ErrBaseDomainRequired = errors.New("BASE_DOMAIN is required")

	// ErrDatabaseURLRequired is returned when DATABASE_URL is not set.
	ErrDatabaseURLRequired = errors.New("DATABASE_URL is required")

	// ErrInvalidPort is returned when PORT is not a valid integer.
	ErrInvalidPort = errors.New("PORT must be a valid integer between 1 and 65535")

	// ErrInvalidBackendsJSON is returned when BACKEND_ROUTES contains invalid JSON.
	ErrInvalidBackendsJSON = errors.New("BACKEND_ROUTES must be valid JSON array")
)

// LoadConfig loads configuration from environment variables.
// It validates required fields and returns an error if validation fails.
func LoadConfig() (*Config, error) {
	config := &Config{
		Port:         getEnvAsInt("PORT", 8080),
		BaseDomain:   os.Getenv("BASE_DOMAIN"),
		LocalDevMode: getEnvAsBool("LOCAL_DEV_MODE", false),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		RedisURL:     os.Getenv("REDIS_URL"),
	}

	// Parse backend routes from JSON
	backendsJSON := os.Getenv("BACKEND_ROUTES")
	if backendsJSON != "" {
		var backends []BackendRoute
		if err := json.Unmarshal([]byte(backendsJSON), &backends); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidBackendsJSON, err)
		}
		config.Backends = backends
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, err
	}

	return config, nil
}

// Validate checks that all required configuration values are set and valid.
func (c *Config) Validate() error {
	if c.BaseDomain == "" {
		return ErrBaseDomainRequired
	}

	if c.DatabaseURL == "" {
		return ErrDatabaseURLRequired
	}

	if c.Port < 1 || c.Port > 65535 {
		return ErrInvalidPort
	}

	return nil
}

// getEnvAsInt returns the environment variable value as int or default.
func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(valueStr)
	if err != nil {
		return defaultValue
	}
	return value
}

// getEnvAsBool returns the environment variable value as bool or default.
func getEnvAsBool(key string, defaultValue bool) bool {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	switch strings.ToLower(valueStr) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return defaultValue
	}
}
