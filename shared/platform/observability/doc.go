// Package observability provides OpenTelemetry tracing and gRPC instrumentation
// for Meridian services.
//
// The package configures an OTLP trace exporter (defaulting to Grafana Alloy at
// alloy:4317) and registers a tracer provider. gRPC servers and clients are
// instrumented via the helpers in this package to propagate trace context across
// service boundaries.
//
// # Environment Variables
//
//   - OTEL_SERVICE_NAME: service name (required)
//   - OTEL_SERVICE_VERSION: service version (default: "unknown")
//   - OTEL_ENVIRONMENT: deployment environment (default: "development")
//   - OTEL_EXPORTER_OTLP_ENDPOINT: OTLP endpoint (default: "alloy:4317")
//   - OTEL_TRACES_SAMPLER_ARG: sampling rate 0.0–1.0 (default: 1.0)
//   - OTEL_TRACES_ENABLED: enable tracing (default: "true")
package observability
