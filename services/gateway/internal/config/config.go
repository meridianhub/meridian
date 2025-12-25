// Package config provides configuration for the gateway service.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config holds the gateway service configuration.
type Config struct {
	// Port is the HTTP port to listen on.
	Port int

	// BaseDomain is the base domain for tenant resolution (e.g., "api.meridianhub.cloud").
	BaseDomain string

	// Backends is a list of backend service configurations.
	Backends []BackendConfig
}

// BackendConfig describes a backend service route.
type BackendConfig struct {
	// Name is the service name for logging/metrics.
	Name string

	// PathPrefix is the URL path prefix to match (e.g., "/accounts").
	PathPrefix string

	// Target is the backend service address (e.g., "current-account:50051").
	Target string
}

// LoadFromEnv loads configuration from environment variables.
func LoadFromEnv() (*Config, error) {
	port := getEnvAsInt("GATEWAY_PORT", 8080)

	baseDomain := getEnvOrDefault("GATEWAY_BASE_DOMAIN", "api.meridianhub.cloud")

	// Load backend configurations from environment
	backends := loadBackends()

	return &Config{
		Port:       port,
		BaseDomain: baseDomain,
		Backends:   backends,
	}, nil
}

// loadBackends loads backend configurations from environment variables.
// Expected format: BACKEND_{NAME}_TARGET and BACKEND_{NAME}_PATH_PREFIX
// Example:
//
//	BACKEND_CURRENT_ACCOUNT_TARGET=current-account:50051
//	BACKEND_CURRENT_ACCOUNT_PATH_PREFIX=/accounts
func loadBackends() []BackendConfig {
	backends := []BackendConfig{}

	// Check for known backends via specific environment variables
	knownBackends := []struct {
		envPrefix string
		name      string
	}{
		{"CURRENT_ACCOUNT_SERVICE", "current-account"},
		{"PARTY_SERVICE", "party"},
		{"PAYMENT_ORDER_SERVICE", "payment-order"},
		{"POSITION_KEEPING_SERVICE", "position-keeping"},
		{"FINANCIAL_ACCOUNTING_SERVICE", "financial-accounting"},
		{"TENANT_SERVICE", "tenant"},
	}

	for _, kb := range knownBackends {
		target := os.Getenv(kb.envPrefix + "_TARGET")
		pathPrefix := os.Getenv(kb.envPrefix + "_PATH_PREFIX")

		if target != "" && pathPrefix != "" {
			backends = append(backends, BackendConfig{
				Name:       kb.name,
				Target:     target,
				PathPrefix: pathPrefix,
			})
		}
	}

	// If no specific backends configured, use defaults for Kubernetes
	if len(backends) == 0 {
		namespace := getEnvOrDefault("K8S_NAMESPACE", "default")
		backends = getDefaultBackends(namespace)
	}

	return backends
}

// getDefaultBackends returns the default backend configuration for Kubernetes deployment.
func getDefaultBackends(namespace string) []BackendConfig {
	suffix := fmt.Sprintf(".%s.svc.cluster.local", namespace)

	return []BackendConfig{
		{
			Name:       "current-account",
			PathPrefix: "/accounts",
			Target:     "current-account" + suffix + ":50051",
		},
		{
			Name:       "party",
			PathPrefix: "/parties",
			Target:     "party" + suffix + ":50055",
		},
		{
			Name:       "payment-order",
			PathPrefix: "/payments",
			Target:     "payment-order" + suffix + ":50054",
		},
		{
			Name:       "position-keeping",
			PathPrefix: "/positions",
			Target:     "position-keeping" + suffix + ":50053",
		},
		{
			Name:       "financial-accounting",
			PathPrefix: "/accounting",
			Target:     "financial-accounting" + suffix + ":50052",
		},
		{
			Name:       "tenant",
			PathPrefix: "/tenants",
			Target:     "tenant" + suffix + ":50056",
		},
	}
}

// getEnvOrDefault returns the environment variable value or default.
func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

// getEnvAsInt returns the environment variable value as int or default.
func getEnvAsInt(key string, defaultValue int) int {
	valueStr := os.Getenv(key)
	if valueStr == "" {
		return defaultValue
	}

	value, err := strconv.Atoi(strings.TrimSpace(valueStr))
	if err != nil {
		return defaultValue
	}
	return value
}
