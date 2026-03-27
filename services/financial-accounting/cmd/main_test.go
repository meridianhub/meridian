package main

import (
	"context"
	"log/slog"
	"testing"

	"github.com/meridianhub/meridian/services/financial-accounting/app"
	"github.com/meridianhub/meridian/services/reference-data/cache"
	"github.com/meridianhub/meridian/shared/platform/env"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/emptypb"
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

func TestNoopEventPublisher_Publish(t *testing.T) {
	publisher := &app.NoopEventPublisher{}
	err := publisher.Publish(context.Background(), &emptypb.Empty{})
	assert.NoError(t, err)
}

func TestNoopEventPublisher_PublishBatch(t *testing.T) {
	publisher := &app.NoopEventPublisher{}
	err := publisher.PublishBatch(context.Background(), nil)
	assert.NoError(t, err)
}

func TestCachedInstrumentResultAdapter_GetBucketKeyProgram(t *testing.T) {
	cached := &cache.CachedInstrument{
		BucketKeyProgram: nil,
	}
	adapter := &app.CachedInstrumentResultAdapter{Cached: cached}
	result := adapter.GetBucketKeyProgram()
	assert.Nil(t, result)
}

func TestStaticErrors_Defined(t *testing.T) {
	assert.NotNil(t, app.ErrBankCashAccountIDRequired)
	assert.NotNil(t, app.ErrBankCashAccountIDInvalid)
	assert.NotNil(t, app.ErrRedisRequiredInProduction)
	assert.NotNil(t, app.ErrKafkaRequiredInProduction)
}

func TestCreateRedisClient_InvalidURL(t *testing.T) {
	t.Setenv("REDIS_URL", "://not-a-valid-url")
	logger := slog.Default()
	client, err := app.CreateRedisClient(logger)
	require.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "invalid REDIS_URL")
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
