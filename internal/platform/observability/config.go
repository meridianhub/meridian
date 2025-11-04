package observability

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

var (
	// ErrServiceNameRequired is returned when OTEL_SERVICE_NAME is not set
	ErrServiceNameRequired = errors.New("OTEL_SERVICE_NAME environment variable is required")

	// ErrOTLPEndpointRequired is returned when OTLP endpoint is not configured
	ErrOTLPEndpointRequired = errors.New("OTLP endpoint is required")

	// ErrInvalidSamplingRate is returned when sampling rate is outside 0.0-1.0 range
	ErrInvalidSamplingRate = errors.New("sampling rate must be between 0.0 and 1.0")
)

// DefaultConfig returns a TracerConfig with sensible defaults
//
// Configuration is loaded from environment variables:
//   - OTEL_SERVICE_NAME: Service name (required)
//   - OTEL_SERVICE_VERSION: Service version (default: "unknown")
//   - OTEL_ENVIRONMENT: Deployment environment (default: "development")
//   - OTEL_EXPORTER_OTLP_ENDPOINT: OTLP endpoint (default: "alloy:4317")
//   - OTEL_TRACES_SAMPLER_ARG: Sampling rate 0.0-1.0 (default: 1.0 for dev, 0.1 for prod)
//   - OTEL_TRACES_ENABLED: Enable tracing (default: "true")
//   - OTEL_EXPORTER_OTLP_INSECURE: Disable TLS (default: "true" for dev, "false" for prod)
//
// Example:
//
//	cfg, err := observability.DefaultConfig()
//	if err != nil {
//	    return fmt.Errorf("failed to load tracer config: %w", err)
//	}
//	tracer, err := observability.NewTracer(ctx, cfg)
func DefaultConfig() (TracerConfig, error) {
	serviceName := getEnvOrDefault("OTEL_SERVICE_NAME", "")
	if serviceName == "" {
		return TracerConfig{}, ErrServiceNameRequired
	}

	environment := getEnvOrDefault("OTEL_ENVIRONMENT", "development")

	// Default sampling rate based on environment
	defaultSamplingRate := "1.0" // 100% for development
	if environment == "production" || environment == "staging" {
		defaultSamplingRate = "0.1" // 10% for production/staging
	}

	samplingRateStr := getEnvOrDefault("OTEL_TRACES_SAMPLER_ARG", defaultSamplingRate)
	samplingRate, err := strconv.ParseFloat(samplingRateStr, 64)
	if err != nil {
		return TracerConfig{}, fmt.Errorf("invalid OTEL_TRACES_SAMPLER_ARG: %w", err)
	}

	if samplingRate < 0.0 || samplingRate > 1.0 {
		return TracerConfig{}, fmt.Errorf("%w: got %f", ErrInvalidSamplingRate, samplingRate)
	}

	enabledStr := getEnvOrDefault("OTEL_TRACES_ENABLED", "true")
	enabled, err := strconv.ParseBool(enabledStr)
	if err != nil {
		return TracerConfig{}, fmt.Errorf("invalid OTEL_TRACES_ENABLED: %w", err)
	}

	// Default TLS based on environment
	// Development: insecure (no TLS) for simplicity
	// Production/Staging: TLS enabled for security
	defaultInsecure := "true" // Development default
	if environment == "production" || environment == "staging" {
		defaultInsecure = "false" // Production requires TLS
	}

	insecureStr := getEnvOrDefault("OTEL_EXPORTER_OTLP_INSECURE", defaultInsecure)
	insecure, err := strconv.ParseBool(insecureStr)
	if err != nil {
		return TracerConfig{}, fmt.Errorf("invalid OTEL_EXPORTER_OTLP_INSECURE: %w", err)
	}

	return TracerConfig{
		ServiceName:    serviceName,
		ServiceVersion: getEnvOrDefault("OTEL_SERVICE_VERSION", "unknown"),
		Environment:    environment,
		OTLPEndpoint:   getEnvOrDefault("OTEL_EXPORTER_OTLP_ENDPOINT", "alloy:4317"),
		SamplingRate:   samplingRate,
		UseTLS:         !insecure, // Convert insecure flag to useTLS
		Enabled:        enabled,
	}, nil
}

// getEnvOrDefault returns the environment variable value or a default if not set
// Trims whitespace from the value before returning
func getEnvOrDefault(key, defaultValue string) string {
	value := os.Getenv(key)
	// Trim whitespace to handle cases like "   " being treated as empty
	value = strings.TrimSpace(value)
	if value != "" {
		return value
	}
	return defaultValue
}

// WithServiceName returns a config with the service name set
func (c TracerConfig) WithServiceName(name string) TracerConfig {
	c.ServiceName = name
	return c
}

// WithServiceVersion returns a config with the service version set
func (c TracerConfig) WithServiceVersion(version string) TracerConfig {
	c.ServiceVersion = version
	return c
}

// WithEnvironment returns a config with the environment set
func (c TracerConfig) WithEnvironment(env string) TracerConfig {
	c.Environment = env
	return c
}

// WithOTLPEndpoint returns a config with the OTLP endpoint set
func (c TracerConfig) WithOTLPEndpoint(endpoint string) TracerConfig {
	c.OTLPEndpoint = endpoint
	return c
}

// WithSamplingRate returns a config with the sampling rate set
func (c TracerConfig) WithSamplingRate(rate float64) TracerConfig {
	c.SamplingRate = rate
	return c
}

// WithUseTLS returns a config with TLS enabled or disabled
func (c TracerConfig) WithUseTLS(useTLS bool) TracerConfig {
	c.UseTLS = useTLS
	return c
}

// WithEnabled returns a config with tracing enabled or disabled
func (c TracerConfig) WithEnabled(enabled bool) TracerConfig {
	c.Enabled = enabled
	return c
}

// Validate checks if the configuration is valid
func (c TracerConfig) Validate() error {
	if c.ServiceName == "" {
		return ErrServiceNameRequired
	}

	if c.OTLPEndpoint == "" {
		return ErrOTLPEndpointRequired
	}

	if c.SamplingRate < 0.0 || c.SamplingRate > 1.0 {
		return fmt.Errorf("%w: got %f", ErrInvalidSamplingRate, c.SamplingRate)
	}

	return nil
}
