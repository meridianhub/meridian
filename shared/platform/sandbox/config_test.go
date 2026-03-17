package sandbox

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, 5*time.Second, cfg.Timeout)
	assert.Equal(t, 64*1024, cfg.MaxScriptSize)
	assert.Equal(t, uint64(1_000_000), cfg.MaxStepsPerExecution)
}

func TestValuationConfig(t *testing.T) {
	cfg := ValuationConfig()

	assert.Equal(t, 5*time.Second, cfg.Timeout)
	assert.Equal(t, 64*1024, cfg.MaxScriptSize)
	assert.Equal(t, uint64(5_000_000), cfg.MaxStepsPerExecution)
}

func TestForecasterConfig(t *testing.T) {
	cfg := ForecasterConfig()

	assert.Equal(t, 10*time.Second, cfg.Timeout)
	assert.Equal(t, 64*1024, cfg.MaxScriptSize)
	assert.Equal(t, uint64(1_000_000), cfg.MaxStepsPerExecution)
}

func TestConfigsShareScriptSize(t *testing.T) {
	// All configs should use the same script size limit (64KB).
	configs := []Config{DefaultConfig(), ValuationConfig(), ForecasterConfig()}
	for _, cfg := range configs {
		assert.Equal(t, 64*1024, cfg.MaxScriptSize)
	}
}
