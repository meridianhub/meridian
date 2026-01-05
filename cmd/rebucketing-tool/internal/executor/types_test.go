package executor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAuditOperation_String(t *testing.T) {
	tests := []struct {
		name     string
		op       AuditOperation
		expected string
	}{
		{
			name:     "soft delete",
			op:       AuditOperationSoftDelete,
			expected: "SOFT_DELETE",
		},
		{
			name:     "insert new",
			op:       AuditOperationInsertNew,
			expected: "INSERT_NEW",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.op.String())
		})
	}
}

func TestAuditOperation_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		op       AuditOperation
		expected bool
	}{
		{
			name:     "soft delete is valid",
			op:       AuditOperationSoftDelete,
			expected: true,
		},
		{
			name:     "insert new is valid",
			op:       AuditOperationInsertNew,
			expected: true,
		},
		{
			name:     "empty is invalid",
			op:       AuditOperation(""),
			expected: false,
		},
		{
			name:     "unknown is invalid",
			op:       AuditOperation("UNKNOWN"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, tt.op.IsValid())
		})
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	require.NotNil(t, config)
	assert.Equal(t, 500, config.BatchSize)
	assert.False(t, config.DryRun)
}

func TestConfig_Validate(t *testing.T) {
	tests := []struct {
		name        string
		config      *Config
		expectError error
	}{
		{
			name: "valid default config",
			config: &Config{
				BatchSize: 500,
				DryRun:    false,
			},
			expectError: nil,
		},
		{
			name: "valid small batch size",
			config: &Config{
				BatchSize: 1,
				DryRun:    true,
			},
			expectError: nil,
		},
		{
			name: "valid max batch size",
			config: &Config{
				BatchSize: 10000,
				DryRun:    false,
			},
			expectError: nil,
		},
		{
			name: "zero batch size",
			config: &Config{
				BatchSize: 0,
				DryRun:    false,
			},
			expectError: ErrInvalidBatchSize,
		},
		{
			name: "negative batch size",
			config: &Config{
				BatchSize: -1,
				DryRun:    false,
			},
			expectError: ErrInvalidBatchSize,
		},
		{
			name: "batch size too large",
			config: &Config{
				BatchSize: 10001,
				DryRun:    false,
			},
			expectError: ErrBatchSizeTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.expectError != nil {
				assert.ErrorIs(t, err, tt.expectError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
