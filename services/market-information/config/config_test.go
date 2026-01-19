package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultECBConfig(t *testing.T) {
	cfg := DefaultECBConfig()

	assert.False(t, cfg.Enabled, "ECB should be disabled by default")
	assert.Empty(t, cfg.Endpoint, "Endpoint should be empty by default (uses client default)")
	assert.Equal(t, "ECB", cfg.SourceCode)
	assert.Equal(t, "ECB_FX", cfg.DatasetCode)
	assert.Equal(t, 24*time.Hour, cfg.Interval)
	assert.Equal(t, 30*time.Second, cfg.Timeout)
	assert.Equal(t, 3, cfg.MaxRetries)
}

func TestLoadECBConfig_Defaults(t *testing.T) {
	// Clear any existing env vars
	envVars := []string{
		"ECB_ENABLED",
		"ECB_ENDPOINT",
		"ECB_SOURCE_CODE",
		"ECB_DATASET_CODE",
		"ECB_INTERVAL",
		"ECB_TIMEOUT",
		"ECB_MAX_RETRIES",
	}
	for _, env := range envVars {
		os.Unsetenv(env)
	}

	cfg := LoadECBConfig()

	assert.False(t, cfg.Enabled)
	assert.Empty(t, cfg.Endpoint)
	assert.Equal(t, "ECB", cfg.SourceCode)
	assert.Equal(t, "ECB_FX", cfg.DatasetCode)
	assert.Equal(t, 24*time.Hour, cfg.Interval)
	assert.Equal(t, 30*time.Second, cfg.Timeout)
	assert.Equal(t, 3, cfg.MaxRetries)
}

func TestLoadECBConfig_FromEnv(t *testing.T) {
	// Set test environment variables
	t.Setenv("ECB_ENABLED", "true")
	t.Setenv("ECB_ENDPOINT", "https://custom.endpoint.com/api")
	t.Setenv("ECB_SOURCE_CODE", "CUSTOM_ECB")
	t.Setenv("ECB_DATASET_CODE", "CUSTOM_FX")
	t.Setenv("ECB_INTERVAL", "12h")
	t.Setenv("ECB_TIMEOUT", "45s")
	t.Setenv("ECB_MAX_RETRIES", "5")

	cfg := LoadECBConfig()

	assert.True(t, cfg.Enabled)
	assert.Equal(t, "https://custom.endpoint.com/api", cfg.Endpoint)
	assert.Equal(t, "CUSTOM_ECB", cfg.SourceCode)
	assert.Equal(t, "CUSTOM_FX", cfg.DatasetCode)
	assert.Equal(t, 12*time.Hour, cfg.Interval)
	assert.Equal(t, 45*time.Second, cfg.Timeout)
	assert.Equal(t, 5, cfg.MaxRetries)
}

func TestLoadECBConfig_InvalidValues_UsesDefaults(t *testing.T) {
	// Set invalid values - should fall back to defaults
	t.Setenv("ECB_ENABLED", "invalid")
	t.Setenv("ECB_INTERVAL", "invalid-duration")
	t.Setenv("ECB_TIMEOUT", "not-a-duration")
	t.Setenv("ECB_MAX_RETRIES", "not-a-number")

	cfg := LoadECBConfig()

	defaults := DefaultECBConfig()
	assert.Equal(t, defaults.Enabled, cfg.Enabled)
	assert.Equal(t, defaults.Interval, cfg.Interval)
	assert.Equal(t, defaults.Timeout, cfg.Timeout)
	assert.Equal(t, defaults.MaxRetries, cfg.MaxRetries)
}

func TestLoadConfig(t *testing.T) {
	// Clear environment
	os.Unsetenv("ECB_ENABLED")

	cfg := LoadConfig()

	// Verify ECB config is loaded
	assert.False(t, cfg.ECB.Enabled)
	assert.Equal(t, "ECB", cfg.ECB.SourceCode)
}
