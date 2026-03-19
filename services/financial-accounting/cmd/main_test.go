package main

import (
	"log/slog"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/stretchr/testify/assert"
)

func TestParseLogLevel(t *testing.T) {
	tests := []struct {
		input    string
		expected slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"Debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"error", slog.LevelError},
		{"ERROR", slog.LevelError},
		{"", slog.LevelInfo},
		{"unknown", slog.LevelInfo},
		{"trace", slog.LevelInfo},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseLogLevel(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

// TestIsProductionEnvironmentDetection tests that environment detection works correctly
// via the shared env package.
func TestIsProductionEnvironmentDetection(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		expected bool
	}{
		{
			name:     "production environment",
			envValue: "production",
			expected: true,
		},
		{
			name:     "prod shorthand",
			envValue: "prod",
			expected: true,
		},
		{
			name:     "development environment",
			envValue: "development",
			expected: false,
		},
		{
			name:     "staging environment",
			envValue: "staging",
			expected: false,
		},
		{
			name:     "empty environment",
			envValue: "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("ENVIRONMENT", tt.envValue)
			result := env.IsProduction()
			assert.Equal(t, tt.expected, result, "IsProduction() should be %v for ENVIRONMENT=%q", tt.expected, tt.envValue)
		})
	}
}

// Note: Full integration tests for production startup requirements would need
// to run the actual service and verify it fails to start without Redis/Kafka.
// These are covered by the unit tests in:
// - shared/platform/env/env_test.go (IsProduction)
// - services/financial-accounting/observability/health_test.go (NoopFallbackChecker)
//
// The production fail-fast behavior is implemented in the run() function of main.go,
// which checks env.IsProduction() before allowing NoOp fallbacks.
//
// To test the actual startup behavior, you would need to:
// 1. Set ENVIRONMENT=production
// 2. Ensure Redis/Kafka are not available
// 3. Verify the service exits with an error
//
// This is typically done via:
// - E2E tests in CI that spin up the service container
// - Manual testing with `ENVIRONMENT=production go run ./services/financial-accounting/cmd`
