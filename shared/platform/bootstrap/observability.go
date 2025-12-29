package bootstrap

import (
	"context"
	"log/slog"
	"time"

	"github.com/meridianhub/meridian/shared/platform/observability"
)

// TracerConfig wraps observability.TracerConfig with service-specific defaults.
// It provides a simplified interface for service bootstrap.
type TracerConfig struct {
	// ServiceName identifies this service in traces.
	ServiceName string

	// ServiceVersion is the version of the service.
	ServiceVersion string

	// Logger is used for tracer lifecycle events.
	Logger *slog.Logger
}

// NewTracer creates an OpenTelemetry tracer using the observability package.
// It loads configuration from environment variables via observability.DefaultConfig()
// and applies service-specific overrides from TracerConfig.
//
// Environment variables (via observability package):
//   - OTEL_ENVIRONMENT: Deployment environment (default: "development")
//   - OTEL_EXPORTER_OTLP_ENDPOINT: OTLP endpoint (default: "alloy:4317")
//   - OTEL_TRACES_SAMPLER_ARG: Sampling rate 0.0-1.0
//   - OTEL_TRACES_ENABLED: Enable tracing (default: "true")
//   - OTEL_EXPORTER_OTLP_INSECURE: Disable TLS (default: "true" for dev)
//
// The ServiceName and ServiceVersion from TracerConfig take precedence over
// environment variables OTEL_SERVICE_NAME and OTEL_SERVICE_VERSION.
func NewTracer(ctx context.Context, cfg TracerConfig) (*observability.Tracer, error) {
	// Load base config from environment - we create a minimal config
	// since we'll override service name/version from TracerConfig
	obsCfg := observability.TracerConfig{
		ServiceName:    cfg.ServiceName,
		ServiceVersion: cfg.ServiceVersion,
	}

	// Try to load environment config for remaining fields
	envCfg, err := observability.DefaultConfig()
	if err != nil {
		// If DefaultConfig fails due to missing OTEL_SERVICE_NAME,
		// we can still proceed since we have the service name from cfg
		if cfg.ServiceName == "" {
			return nil, err
		}
		// Use sensible defaults for remaining fields
		obsCfg.Environment = "development"
		obsCfg.OTLPEndpoint = "alloy:4317"
		obsCfg.SamplingRate = 1.0
		obsCfg.UseTLS = false
		obsCfg.Enabled = true
	} else {
		// Use environment config but override service-specific fields
		obsCfg.Environment = envCfg.Environment
		obsCfg.OTLPEndpoint = envCfg.OTLPEndpoint
		obsCfg.SamplingRate = envCfg.SamplingRate
		obsCfg.UseTLS = envCfg.UseTLS
		obsCfg.Enabled = envCfg.Enabled
	}

	tracer, err := observability.NewTracer(ctx, obsCfg)
	if err != nil {
		return nil, err
	}

	if cfg.Logger != nil {
		cfg.Logger.Info("tracer initialized",
			"service_name", cfg.ServiceName,
			"service_version", cfg.ServiceVersion,
			"environment", obsCfg.Environment,
			"endpoint", obsCfg.OTLPEndpoint,
			"enabled", obsCfg.Enabled)
	}

	return tracer, nil
}

// ShutdownTracer gracefully shuts down the tracer with a 5-second timeout.
// It logs any errors encountered during shutdown if a logger is provided.
func ShutdownTracer(tracer *observability.Tracer, logger *slog.Logger) {
	if tracer == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := tracer.Shutdown(ctx); err != nil {
		if logger != nil {
			logger.Error("failed to shutdown tracer", "error", err)
		}
	} else if logger != nil {
		logger.Info("tracer shutdown complete")
	}
}
