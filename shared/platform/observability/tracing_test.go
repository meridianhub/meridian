package observability_test

import (
	"context"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/observability"
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
		WithUseTLS(true).
		WithEnabled(true)

	assert.Equal(t, "test-service", cfg.ServiceName)
	assert.Equal(t, "1.0.0", cfg.ServiceVersion)
	assert.Equal(t, "test", cfg.Environment)
	assert.Equal(t, "collector:4317", cfg.OTLPEndpoint)
	assert.Equal(t, 0.5, cfg.SamplingRate)
	assert.True(t, cfg.UseTLS)
	assert.True(t, cfg.Enabled)
}

// Defensive Tests - ADR-008 Compliance

func TestDefaultConfig_DefensiveTests(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantErr   bool
		check     func(*testing.T, observability.TracerConfig)
		rationale string
	}{
		{
			name: "whitespace-only service name",
			env: map[string]string{
				"OTEL_SERVICE_NAME": "   ",
			},
			wantErr:   true,
			rationale: "Whitespace-only values should be treated as empty and rejected",
		},
		{
			name: "sampling rate exactly 0.0 (NeverSample)",
			env: map[string]string{
				"OTEL_SERVICE_NAME":       "test",
				"OTEL_TRACES_SAMPLER_ARG": "0.0",
			},
			wantErr: false,
			check: func(t *testing.T, cfg observability.TracerConfig) {
				assert.Equal(t, 0.0, cfg.SamplingRate)
			},
			rationale: "Zero sampling rate is valid for disabling all traces",
		},
		{
			name: "sampling rate exactly 1.0 (AlwaysSample)",
			env: map[string]string{
				"OTEL_SERVICE_NAME":       "test",
				"OTEL_TRACES_SAMPLER_ARG": "1.0",
			},
			wantErr: false,
			check: func(t *testing.T, cfg observability.TracerConfig) {
				assert.Equal(t, 1.0, cfg.SamplingRate)
			},
			rationale: "100% sampling is valid for development",
		},
		{
			name: "negative sampling rate",
			env: map[string]string{
				"OTEL_SERVICE_NAME":       "test",
				"OTEL_TRACES_SAMPLER_ARG": "-0.5",
			},
			wantErr:   true,
			rationale: "Negative sampling rates are invalid",
		},
		{
			name: "sampling rate above 1.0",
			env: map[string]string{
				"OTEL_SERVICE_NAME":       "test",
				"OTEL_TRACES_SAMPLER_ARG": "1.5",
			},
			wantErr:   true,
			rationale: "Sampling rates above 1.0 are invalid",
		},
		{
			name: "invalid boolean for OTEL_TRACES_ENABLED",
			env: map[string]string{
				"OTEL_SERVICE_NAME":   "test",
				"OTEL_TRACES_ENABLED": "yes",
			},
			wantErr:   true,
			rationale: "Non-boolean values should be rejected",
		},
		{
			name: "production environment defaults to TLS enabled",
			env: map[string]string{
				"OTEL_SERVICE_NAME": "test",
				"OTEL_ENVIRONMENT":  "production",
			},
			wantErr: false,
			check: func(t *testing.T, cfg observability.TracerConfig) {
				assert.True(t, cfg.UseTLS, "Production should default to TLS enabled")
			},
			rationale: "Production environments should use TLS by default for security",
		},
		{
			name: "development environment defaults to TLS disabled",
			env: map[string]string{
				"OTEL_SERVICE_NAME": "test",
				"OTEL_ENVIRONMENT":  "development",
			},
			wantErr: false,
			check: func(t *testing.T, cfg observability.TracerConfig) {
				assert.False(t, cfg.UseTLS, "Development should default to TLS disabled")
			},
			rationale: "Development environments can use insecure connections for simplicity",
		},
		{
			name: "explicit TLS override",
			env: map[string]string{
				"OTEL_SERVICE_NAME":           "test",
				"OTEL_ENVIRONMENT":            "production",
				"OTEL_EXPORTER_OTLP_INSECURE": "true",
			},
			wantErr: false,
			check: func(t *testing.T, cfg observability.TracerConfig) {
				assert.False(t, cfg.UseTLS, "Explicit insecure flag should override environment default")
			},
			rationale: "Allow explicit override of TLS setting for testing",
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
				assert.Error(t, err, "Rationale: %s", tt.rationale)
				return
			}

			require.NoError(t, err, "Rationale: %s", tt.rationale)
			if tt.check != nil {
				tt.check(t, cfg)
			}
		})
	}
}

func TestNewTracer_ValidatesConfig(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		config    observability.TracerConfig
		wantErr   bool
		rationale string
	}{
		{
			name: "invalid config rejected by NewTracer",
			config: observability.TracerConfig{
				ServiceName:  "", // Empty - invalid
				OTLPEndpoint: "alloy:4317",
			},
			wantErr:   true,
			rationale: "NewTracer should validate config and reject empty service name",
		},
		{
			name: "config with invalid sampling rate rejected",
			config: observability.TracerConfig{
				ServiceName:  "test",
				OTLPEndpoint: "alloy:4317",
				SamplingRate: 2.0, // Invalid
			},
			wantErr:   true,
			rationale: "NewTracer should validate sampling rate bounds",
		},
		{
			name: "valid config accepted",
			config: observability.TracerConfig{
				ServiceName:  "test",
				OTLPEndpoint: "alloy:4317",
				SamplingRate: 0.5,
				Enabled:      false, // Disabled to avoid OTLP connection
			},
			wantErr:   false,
			rationale: "Valid config should be accepted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := observability.NewTracer(ctx, tt.config)

			if tt.wantErr {
				assert.Error(t, err, "Rationale: %s", tt.rationale)
			} else {
				assert.NoError(t, err, "Rationale: %s", tt.rationale)
			}
		})
	}
}
