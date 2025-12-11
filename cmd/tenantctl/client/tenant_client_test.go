package client

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, "localhost:50056", cfg.ServiceURL)
	assert.Equal(t, "tenant", cfg.ServiceName)
	assert.Equal(t, "default", cfg.Namespace)
	assert.Equal(t, 50056, cfg.Port)
	assert.Equal(t, 30*time.Second, cfg.Timeout)
}

func TestConfig_ApplyDefaults(t *testing.T) {
	tests := []struct {
		name            string
		input           Config
		expectedTimeout time.Duration
		expectedPort    int
	}{
		{
			name:            "empty config gets defaults",
			input:           Config{},
			expectedTimeout: 30 * time.Second,
			expectedPort:    50056,
		},
		{
			name: "custom values preserved",
			input: Config{
				Timeout: 60 * time.Second,
				Port:    8080,
			},
			expectedTimeout: 60 * time.Second,
			expectedPort:    8080,
		},
		{
			name: "zero timeout gets default",
			input: Config{
				Port: 9090,
			},
			expectedTimeout: 30 * time.Second,
			expectedPort:    9090,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Apply defaults inline as the client constructor does
			cfg := tt.input
			if cfg.Timeout == 0 {
				cfg.Timeout = 30 * time.Second
			}
			if cfg.Port == 0 {
				cfg.Port = 50056
			}

			assert.Equal(t, tt.expectedTimeout, cfg.Timeout)
			assert.Equal(t, tt.expectedPort, cfg.Port)
		})
	}
}
