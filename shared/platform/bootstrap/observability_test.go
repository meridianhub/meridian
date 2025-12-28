package bootstrap

import (
	"context"
	"log/slog"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewTracer(t *testing.T) {
	t.Run("creates tracer with service config", func(t *testing.T) {
		// Clear OTEL env vars to use defaults
		os.Unsetenv("OTEL_SERVICE_NAME")
		os.Unsetenv("OTEL_TRACES_ENABLED")

		ctx := context.Background()
		cfg := TracerConfig{
			ServiceName:    "test-service",
			ServiceVersion: "1.0.0",
		}

		// NewTracer should succeed since we provide service name
		tracer, err := NewTracer(ctx, cfg)
		require.NoError(t, err)
		require.NotNil(t, tracer)

		// Clean up
		ShutdownTracer(tracer, nil)
	})

	t.Run("fails when service name is missing and not in env", func(t *testing.T) {
		os.Unsetenv("OTEL_SERVICE_NAME")

		ctx := context.Background()
		cfg := TracerConfig{
			ServiceName: "", // Empty service name
		}

		_, err := NewTracer(ctx, cfg)
		require.Error(t, err)
	})

	t.Run("uses environment config when available", func(t *testing.T) {
		t.Setenv("OTEL_SERVICE_NAME", "env-service")
		t.Setenv("OTEL_ENVIRONMENT", "staging")
		t.Setenv("OTEL_TRACES_ENABLED", "true")

		ctx := context.Background()
		cfg := TracerConfig{
			ServiceName:    "override-service", // Should take precedence
			ServiceVersion: "2.0.0",
		}

		tracer, err := NewTracer(ctx, cfg)
		require.NoError(t, err)
		require.NotNil(t, tracer)

		ShutdownTracer(tracer, nil)
	})

	t.Run("logs initialization when logger provided", func(t *testing.T) {
		os.Unsetenv("OTEL_SERVICE_NAME")

		ctx := context.Background()
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

		cfg := TracerConfig{
			ServiceName:    "logged-service",
			ServiceVersion: "1.0.0",
			Logger:         logger,
		}

		tracer, err := NewTracer(ctx, cfg)
		require.NoError(t, err)
		require.NotNil(t, tracer)

		ShutdownTracer(tracer, logger)
	})
}

func TestShutdownTracer(t *testing.T) {
	t.Run("handles nil tracer gracefully", func(_ *testing.T) {
		// Should not panic
		ShutdownTracer(nil, nil)
	})

	t.Run("handles nil logger gracefully", func(t *testing.T) {
		os.Unsetenv("OTEL_SERVICE_NAME")

		ctx := context.Background()
		cfg := TracerConfig{
			ServiceName:    "test-service",
			ServiceVersion: "1.0.0",
		}

		tracer, err := NewTracer(ctx, cfg)
		require.NoError(t, err)

		// Should not panic with nil logger
		ShutdownTracer(tracer, nil)
	})

	t.Run("logs shutdown when logger provided", func(t *testing.T) {
		os.Unsetenv("OTEL_SERVICE_NAME")

		ctx := context.Background()
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

		cfg := TracerConfig{
			ServiceName:    "test-service",
			ServiceVersion: "1.0.0",
		}

		tracer, err := NewTracer(ctx, cfg)
		require.NoError(t, err)

		// Should log shutdown
		ShutdownTracer(tracer, logger)
	})
}

func TestTracerConfig(t *testing.T) {
	t.Run("fields are accessible", func(t *testing.T) {
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

		cfg := TracerConfig{
			ServiceName:    "my-service",
			ServiceVersion: "3.2.1",
			Logger:         logger,
		}

		assert.Equal(t, "my-service", cfg.ServiceName)
		assert.Equal(t, "3.2.1", cfg.ServiceVersion)
		assert.NotNil(t, cfg.Logger)
	})
}
