package observability

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultConfig_requires_service_name(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")

	_, err := DefaultConfig()
	assert.ErrorIs(t, err, ErrServiceNameRequired)
}

func TestDefaultConfig_development_defaults(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "test-service")
	t.Setenv("OTEL_ENVIRONMENT", "development")
	// Clear any overrides
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "")

	cfg, err := DefaultConfig()
	require.NoError(t, err)

	assert.Equal(t, "test-service", cfg.ServiceName)
	assert.Equal(t, "unknown", cfg.ServiceVersion)
	assert.Equal(t, "development", cfg.Environment)
	assert.Equal(t, "alloy:4317", cfg.OTLPEndpoint)
	assert.Equal(t, 1.0, cfg.SamplingRate) // 100% for dev
	assert.False(t, cfg.UseTLS)            // insecure for dev
	assert.True(t, cfg.Enabled)
}

func TestDefaultConfig_production_defaults(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "test-service")
	t.Setenv("OTEL_ENVIRONMENT", "production")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "")

	cfg, err := DefaultConfig()
	require.NoError(t, err)

	assert.Equal(t, 0.1, cfg.SamplingRate) // 10% for prod
	assert.True(t, cfg.UseTLS)             // TLS for prod
}

func TestDefaultConfig_staging_defaults(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "test-service")
	t.Setenv("OTEL_ENVIRONMENT", "staging")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "")

	cfg, err := DefaultConfig()
	require.NoError(t, err)

	assert.Equal(t, 0.1, cfg.SamplingRate)
	assert.True(t, cfg.UseTLS)
}

func TestDefaultConfig_custom_env_vars(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "my-svc")
	t.Setenv("OTEL_SERVICE_VERSION", "1.2.3")
	t.Setenv("OTEL_ENVIRONMENT", "development")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "0.5")
	t.Setenv("OTEL_TRACES_ENABLED", "false")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "false")

	cfg, err := DefaultConfig()
	require.NoError(t, err)

	assert.Equal(t, "my-svc", cfg.ServiceName)
	assert.Equal(t, "1.2.3", cfg.ServiceVersion)
	assert.Equal(t, "localhost:4317", cfg.OTLPEndpoint)
	assert.Equal(t, 0.5, cfg.SamplingRate)
	assert.False(t, cfg.Enabled)
	assert.True(t, cfg.UseTLS) // insecure=false -> useTLS=true
}

func TestDefaultConfig_invalid_sampling_rate(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "test-service")
	t.Setenv("OTEL_ENVIRONMENT", "development")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1.5")

	_, err := DefaultConfig()
	assert.ErrorIs(t, err, ErrInvalidSamplingRate)
}

func TestDefaultConfig_negative_sampling_rate(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "test-service")
	t.Setenv("OTEL_ENVIRONMENT", "development")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "-0.1")

	_, err := DefaultConfig()
	assert.ErrorIs(t, err, ErrInvalidSamplingRate)
}

func TestDefaultConfig_non_numeric_sampling_rate(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "test-service")
	t.Setenv("OTEL_ENVIRONMENT", "development")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "abc")

	_, err := DefaultConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid OTEL_TRACES_SAMPLER_ARG")
}

func TestDefaultConfig_invalid_enabled_value(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "test-service")
	t.Setenv("OTEL_ENVIRONMENT", "development")
	t.Setenv("OTEL_TRACES_ENABLED", "maybe")

	_, err := DefaultConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid OTEL_TRACES_ENABLED")
}

func TestDefaultConfig_invalid_insecure_value(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "test-service")
	t.Setenv("OTEL_ENVIRONMENT", "development")
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "maybe")

	_, err := DefaultConfig()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid OTEL_EXPORTER_OTLP_INSECURE")
}

func TestTracerConfig_Validate_valid(t *testing.T) {
	cfg := TracerConfig{
		ServiceName:  "test",
		OTLPEndpoint: "localhost:4317",
		SamplingRate: 0.5,
	}
	assert.NoError(t, cfg.Validate())
}

func TestTracerConfig_Validate_missing_service_name(t *testing.T) {
	cfg := TracerConfig{
		OTLPEndpoint: "localhost:4317",
		SamplingRate: 0.5,
	}
	assert.ErrorIs(t, cfg.Validate(), ErrServiceNameRequired)
}

func TestTracerConfig_Validate_missing_endpoint(t *testing.T) {
	cfg := TracerConfig{
		ServiceName:  "test",
		SamplingRate: 0.5,
	}
	assert.ErrorIs(t, cfg.Validate(), ErrOTLPEndpointRequired)
}

func TestTracerConfig_Validate_invalid_sampling_rate(t *testing.T) {
	cfg := TracerConfig{
		ServiceName:  "test",
		OTLPEndpoint: "localhost:4317",
		SamplingRate: 1.5,
	}
	assert.ErrorIs(t, cfg.Validate(), ErrInvalidSamplingRate)
}

func TestTracerConfig_Validate_boundary_sampling_rates(t *testing.T) {
	base := TracerConfig{
		ServiceName:  "test",
		OTLPEndpoint: "localhost:4317",
	}

	// 0.0 is valid
	cfg := base
	cfg.SamplingRate = 0.0
	assert.NoError(t, cfg.Validate())

	// 1.0 is valid
	cfg.SamplingRate = 1.0
	assert.NoError(t, cfg.Validate())
}

func TestTracerConfig_WithBuilders(t *testing.T) {
	cfg := TracerConfig{}.
		WithServiceName("svc").
		WithServiceVersion("1.0").
		WithEnvironment("prod").
		WithOTLPEndpoint("host:4317").
		WithSamplingRate(0.5).
		WithUseTLS(true).
		WithEnabled(false)

	assert.Equal(t, "svc", cfg.ServiceName)
	assert.Equal(t, "1.0", cfg.ServiceVersion)
	assert.Equal(t, "prod", cfg.Environment)
	assert.Equal(t, "host:4317", cfg.OTLPEndpoint)
	assert.Equal(t, 0.5, cfg.SamplingRate)
	assert.True(t, cfg.UseTLS)
	assert.False(t, cfg.Enabled)
}
