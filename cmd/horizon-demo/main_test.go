package main

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	assert.Equal(t, "localhost:50051", cfg.Target)
	assert.Equal(t, 30*time.Millisecond, cfg.Timeout)
	assert.Equal(t, int64(10000), cfg.Amount)
	assert.Equal(t, "./integrity_report.json", cfg.Output)
	assert.False(t, cfg.Verbose)
	assert.False(t, cfg.NoCleanup)
}

func TestValidateConfig_Valid(t *testing.T) {
	tests := []struct {
		name string
		cfg  *Config
	}{
		{
			name: "default config",
			cfg:  DefaultConfig(),
		},
		{
			name: "minimum timeout",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 5 * time.Millisecond,
				Amount:  100,
				Output:  "./report.json",
			},
		},
		{
			name: "maximum timeout",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 10 * time.Second,
				Amount:  100,
				Output:  "./report.json",
			},
		},
		{
			name: "large amount",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 30 * time.Millisecond,
				Amount:  100000000, // GBP 1,000,000.00
				Output:  "./report.json",
			},
		},
		{
			name: "custom target",
			cfg: &Config{
				Target:  "payment-order.default.svc.cluster.local:50054",
				Timeout: 30 * time.Millisecond,
				Amount:  10000,
				Output:  "/tmp/integrity_report.json",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			assert.NoError(t, err)
		})
	}
}

func TestValidateConfig_Invalid(t *testing.T) {
	tests := []struct {
		name      string
		cfg       *Config
		wantError error
	}{
		{
			name: "empty target",
			cfg: &Config{
				Target:  "",
				Timeout: 30 * time.Millisecond,
				Amount:  10000,
				Output:  "./report.json",
			},
			wantError: ErrTargetEmpty,
		},
		{
			name: "zero timeout",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 0,
				Amount:  10000,
				Output:  "./report.json",
			},
			wantError: ErrTimeoutNotPositive,
		},
		{
			name: "negative timeout",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: -1 * time.Second,
				Amount:  10000,
				Output:  "./report.json",
			},
			wantError: ErrTimeoutNotPositive,
		},
		{
			name: "timeout too short",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 1 * time.Millisecond,
				Amount:  10000,
				Output:  "./report.json",
			},
			wantError: ErrTimeoutTooShort,
		},
		{
			name: "timeout too long",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 11 * time.Second,
				Amount:  10000,
				Output:  "./report.json",
			},
			wantError: ErrTimeoutTooLong,
		},
		{
			name: "zero amount",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 30 * time.Millisecond,
				Amount:  0,
				Output:  "./report.json",
			},
			wantError: ErrAmountNotPositive,
		},
		{
			name: "negative amount",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 30 * time.Millisecond,
				Amount:  -100,
				Output:  "./report.json",
			},
			wantError: ErrAmountNotPositive,
		},
		{
			name: "amount exceeds maximum",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 30 * time.Millisecond,
				Amount:  100000001, // Over GBP 1,000,000.00
				Output:  "./report.json",
			},
			wantError: ErrAmountTooLarge,
		},
		{
			name: "empty output",
			cfg: &Config{
				Target:  "localhost:50051",
				Timeout: 30 * time.Millisecond,
				Amount:  10000,
				Output:  "",
			},
			wantError: ErrOutputEmpty,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			require.Error(t, err)
			assert.True(t, errors.Is(err, tt.wantError), "expected error %v, got %v", tt.wantError, err)
		})
	}
}

func TestValidateConfig_BoundaryConditions(t *testing.T) {
	// Test exact boundary values
	t.Run("minimum valid timeout boundary", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 5 * time.Millisecond, // Exact minimum
			Amount:  1,
			Output:  "./report.json",
		}
		assert.NoError(t, validateConfig(cfg))
	})

	t.Run("just below minimum timeout", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 4 * time.Millisecond, // Just below minimum
			Amount:  1,
			Output:  "./report.json",
		}
		assert.Error(t, validateConfig(cfg))
	})

	t.Run("maximum valid timeout boundary", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 10 * time.Second, // Exact maximum
			Amount:  1,
			Output:  "./report.json",
		}
		assert.NoError(t, validateConfig(cfg))
	})

	t.Run("just above maximum timeout", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 10*time.Second + time.Millisecond, // Just above maximum
			Amount:  1,
			Output:  "./report.json",
		}
		assert.Error(t, validateConfig(cfg))
	})

	t.Run("minimum valid amount", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 30 * time.Millisecond,
			Amount:  1, // Minimum valid (1 pence)
			Output:  "./report.json",
		}
		assert.NoError(t, validateConfig(cfg))
	})

	t.Run("maximum valid amount", func(t *testing.T) {
		cfg := &Config{
			Target:  "localhost:50051",
			Timeout: 30 * time.Millisecond,
			Amount:  100000000, // Maximum valid (GBP 1,000,000.00)
			Output:  "./report.json",
		}
		assert.NoError(t, validateConfig(cfg))
	})
}
