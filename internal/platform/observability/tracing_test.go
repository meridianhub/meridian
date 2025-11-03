package observability_test

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/internal/platform/observability"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		check   func(*testing.T, observability.TracerConfig)
	}{
		{
			name: "minimal configuration",
			env: map[string]string{
				"OTEL_SERVICE_NAME": "test-service",
			},
			wantErr: false,
			check: func(t *testing.T, cfg observability.TracerConfig) {
				assert.Equal(t, "test-service", cfg.ServiceName)
				assert.Equal(t, "unknown", cfg.ServiceVersion)
				assert.Equal(t, "development", cfg.Environment)
				assert.Equal(t, "alloy:4317", cfg.OTLPEndpoint)
				assert.Equal(t, 1.0, cfg.SamplingRate) // Development default
				assert.True(t, cfg.Enabled)
			},
		},
		{
			name: "production configuration",
			env: map[string]string{
				"OTEL_SERVICE_NAME":           "prod-service",
				"OTEL_SERVICE_VERSION":        "1.2.3",
				"OTEL_ENVIRONMENT":            "production",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "collector:4317",
				"OTEL_TRACES_SAMPLER_ARG":     "0.1",
				"OTEL_TRACES_ENABLED":         "true",
			},
			wantErr: false,
			check: func(t *testing.T, cfg observability.TracerConfig) {
				assert.Equal(t, "prod-service", cfg.ServiceName)
				assert.Equal(t, "1.2.3", cfg.ServiceVersion)
				assert.Equal(t, "production", cfg.Environment)
				assert.Equal(t, "collector:4317", cfg.OTLPEndpoint)
				assert.Equal(t, 0.1, cfg.SamplingRate)
				assert.True(t, cfg.Enabled)
			},
		},
		{
			name:    "missing service name",
			env:     map[string]string{},
			wantErr: true,
		},
		{
			name: "invalid sampling rate",
			env: map[string]string{
				"OTEL_SERVICE_NAME":       "test-service",
				"OTEL_TRACES_SAMPLER_ARG": "invalid",
			},
			wantErr: true,
		},
		{
			name: "sampling rate out of range",
			env: map[string]string{
				"OTEL_SERVICE_NAME":       "test-service",
				"OTEL_TRACES_SAMPLER_ARG": "1.5",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables
			for key, value := range tt.env {
				t.Setenv(key, value)
			}

			cfg, err := observability.DefaultConfig()

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestTracerConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  observability.TracerConfig
		wantErr bool
	}{
		{
			name: "valid configuration",
			config: observability.TracerConfig{
				ServiceName:  "test-service",
				OTLPEndpoint: "alloy:4317",
				SamplingRate: 0.5,
			},
			wantErr: false,
		},
		{
			name: "missing service name",
			config: observability.TracerConfig{
				OTLPEndpoint: "alloy:4317",
				SamplingRate: 0.5,
			},
			wantErr: true,
		},
		{
			name: "missing OTLP endpoint",
			config: observability.TracerConfig{
				ServiceName:  "test-service",
				SamplingRate: 0.5,
			},
			wantErr: true,
		},
		{
			name: "sampling rate too low",
			config: observability.TracerConfig{
				ServiceName:  "test-service",
				OTLPEndpoint: "alloy:4317",
				SamplingRate: -0.1,
			},
			wantErr: true,
		},
		{
			name: "sampling rate too high",
			config: observability.TracerConfig{
				ServiceName:  "test-service",
				OTLPEndpoint: "alloy:4317",
				SamplingRate: 1.1,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestNewTracer_Disabled(t *testing.T) {
	ctx := context.Background()

	cfg := observability.TracerConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "alloy:4317",
		SamplingRate: 1.0,
		Enabled:      false,
	}

	tracer, err := observability.NewTracer(ctx, cfg)
	require.NoError(t, err)
	assert.NotNil(t, tracer)

	// Should be able to start spans even when disabled (no-op)
	_, span := tracer.Start(ctx, "test-span")
	defer span.End()

	assert.NotNil(t, span)
}

func TestTracer_SpanOperations(t *testing.T) {
	ctx := context.Background()

	// Create disabled tracer for testing (doesn't require OTLP endpoint)
	cfg := observability.TracerConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "alloy:4317",
		SamplingRate: 1.0,
		Enabled:      false,
	}

	tracer, err := observability.NewTracer(ctx, cfg)
	require.NoError(t, err)

	// Test basic span operations
	ctx, span := tracer.Start(ctx, "test-operation")
	defer span.End()

	// Test error recording
	testErr := assert.AnError
	tracer.RecordError(ctx, testErr)

	// Test event addition
	tracer.AddEvent(ctx, "test-event")

	// Test attribute setting
	tracer.SetAttributes(ctx)

	// No assertions - just verify no panics occur
}

func TestTracer_Shutdown(t *testing.T) {
	ctx := context.Background()

	cfg := observability.TracerConfig{
		ServiceName:  "test-service",
		OTLPEndpoint: "alloy:4317",
		SamplingRate: 1.0,
		Enabled:      false,
	}

	tracer, err := observability.NewTracer(ctx, cfg)
	require.NoError(t, err)

	// Should shutdown without error
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	err = tracer.Shutdown(ctx)
	assert.NoError(t, err)
}

func TestTracerConfig_WithMethods(t *testing.T) {
	cfg := observability.TracerConfig{}

	cfg = cfg.WithServiceName("test-service").
		WithServiceVersion("1.0.0").
		WithEnvironment("test").
		WithOTLPEndpoint("collector:4317").
		WithSamplingRate(0.5).
		WithEnabled(true)

	assert.Equal(t, "test-service", cfg.ServiceName)
	assert.Equal(t, "1.0.0", cfg.ServiceVersion)
	assert.Equal(t, "test", cfg.Environment)
	assert.Equal(t, "collector:4317", cfg.OTLPEndpoint)
	assert.Equal(t, 0.5, cfg.SamplingRate)
	assert.True(t, cfg.Enabled)
}
