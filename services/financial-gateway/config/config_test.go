package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg := LoadConfig()

	assert.NotEmpty(t, cfg.GRPCPort)
	assert.NotEmpty(t, cfg.HTTPPort)
	assert.Equal(t, "info", cfg.LogLevel)
	assert.NotZero(t, cfg.CircuitBreaker.Timeout)
	assert.Equal(t, 5, cfg.CircuitBreaker.FailureThreshold)
	assert.Equal(t, float64(100), cfg.RateLimit.RPS)
	assert.Equal(t, 10, cfg.RateLimit.Burst)
}
