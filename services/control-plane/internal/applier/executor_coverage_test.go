package applier

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- Error sentinel tests ---

func TestExecutorErrorSentinels(t *testing.T) {
	assert.EqualError(t, ErrSagaNotFound, "apply_manifest saga definition not found")
	assert.EqualError(t, ErrSagaFailed, "saga execution failed")
	assert.EqualError(t, ErrNilInput, "apply manifest: input is nil")
	assert.EqualError(t, ErrMissingTenantID, "apply manifest: tenant_id is required")
	assert.EqualError(t, ErrPoolRequired, "manifest executor: pool is required")
	assert.EqualError(t, ErrExecutorNotConfigured, "executor not configured: cannot execute non-dry-run apply")
	assert.EqualError(t, ErrOperationalGatewayNotConfigured, "operational_gateway service not configured")
}

// --- parseManifestVersion extended cases ---

func TestParseManifestVersion_Extended(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int
	}{
		{"leading_zero", "01", 1},
		{"multi_digit_major", "123.4.5", 123},
		{"single_digit", "5", 5},
		{"only_dots", "...", 1},
		{"dash_version", "1-beta", 1},
		{"v_prefix", "v2", 1},    // 'v' is not a digit, breaks immediately
		{"zero_version", "0", 1}, // zero returns default 1
		{"zero_dot", "0.1.0", 1}, // leading 0, returns default 1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, parseManifestVersion(tt.input))
		})
	}
}

// --- NewManifestExecutor with logger ---

func TestNewManifestExecutor_WithLogger(t *testing.T) {
	executor := NewManifestExecutor(ManifestExecutorConfig{})
	assert.NotNil(t, executor)
	assert.NotNil(t, executor.logger)
	assert.Nil(t, executor.pool)
	assert.Nil(t, executor.runner)
}

// --- buildSagaInput: verify all keys present ---

func TestBuildSagaInput_AllKeysPresent(t *testing.T) {
	executor := &ManifestExecutor{}

	input := &ApplyManifestInput{
		ManifestVersion: "1",
	}

	sagaInput := executor.buildSagaInput(input)

	// Verify all expected top-level keys exist
	expectedKeys := []string{
		"manifest_version",
		"instruments",
		"account_types",
		"market_data_sources",
		"market_data_sets",
		"valuation_rules",
		"organizations",
		"internal_accounts",
		"saga_definitions",
		"provider_connections",
		"instruction_routes",
	}

	for _, key := range expectedKeys {
		_, ok := sagaInput[key]
		assert.True(t, ok, "expected key %q in saga input", key)
	}
}

// --- ApplyManifestResult fields ---

func TestApplyManifestResult_Fields(t *testing.T) {
	result := &ApplyManifestResult{
		Status:  "applied",
		Version: "1.0",
		Error:   "",
	}
	assert.Equal(t, "applied", result.Status)
	assert.Equal(t, "1.0", result.Version)
	assert.Empty(t, result.Error)
}

func TestApplyManifestResult_FailedFields(t *testing.T) {
	result := &ApplyManifestResult{
		Status:  "failed",
		Version: "1.0",
		Error:   "saga step 3 failed",
	}
	assert.Equal(t, "failed", result.Status)
	assert.Equal(t, "saga step 3 failed", result.Error)
}
