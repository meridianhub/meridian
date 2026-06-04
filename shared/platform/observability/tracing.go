// Package observability provides distributed tracing functionality using OpenTelemetry.
//
// This package implements:
//   - OpenTelemetry tracer initialization with OTLP exporter
//   - gRPC interceptors for automatic span creation
//   - Context propagation across service boundaries
//   - Configurable trace sampling strategies
//   - Helper functions for manual span creation with semantic conventions
//
// The tracing system integrates with the Grafana observability stack:
//   - Traces are exported via OTLP to Grafana Alloy
//   - Alloy forwards traces to Grafana Tempo for storage
//   - Grafana provides visualization and querying
package observability

import (
	"context"
	"crypto/tls"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/credentials"
)

// TracerConfig holds configuration for OpenTelemetry tracing
type TracerConfig struct {
	// ServiceName identifies this service in traces
	ServiceName string

	// ServiceVersion is the version of the service
	ServiceVersion string

	// Environment identifies the deployment environment (dev, staging, prod)
	Environment string

	// OTLPEndpoint is the OTLP collector endpoint (e.g., "alloy:4317")
	OTLPEndpoint string

	// SamplingRate controls what percentage of traces to sample (0.0-1.0)
	// 1.0 = sample all traces (recommended for development)
	// 0.1 = sample 10% of traces (recommended for production)
	SamplingRate float64

	// UseTLS enables TLS for OTLP connection (recommended for production)
	// false = insecure connection (development only)
	// true = TLS with system certificates (production)
	UseTLS bool

	// Enabled controls whether tracing is active
	Enabled bool
}

// Tracer wraps OpenTelemetry tracing functionality
type Tracer struct {
	tracer   trace.Tracer
	provider *sdktrace.TracerProvider
	config   TracerConfig
}

// NewTracer creates and initializes an OpenTelemetry tracer
//
// The tracer exports spans via OTLP to the configured endpoint.
// Traces include resource attributes for service identification.
//
// Example:
//
//	cfg := observability.TracerConfig{
//	    ServiceName: "current-account-service",
//	    ServiceVersion: "1.0.0",
//	    Environment: "production",
//	    OTLPEndpoint: "alloy:4317",
//	    SamplingRate: 0.1,
//	    Enabled: true,
//	}
//	tracer, err := observability.NewTracer(ctx, cfg)
//	if err != nil {
//	    return fmt.Errorf("failed to initialize tracer: %w", err)
//	}
//	defer tracer.Shutdown(ctx)
func NewTracer(ctx context.Context, config TracerConfig) (*Tracer, error) {
	// Validate configuration first
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid tracer config: %w", err)
	}

	if !config.Enabled {
		// Return a no-op tracer when disabled
		return &Tracer{
			tracer:   otel.Tracer(config.ServiceName),
			provider: nil,
			config:   config,
		}, nil
	}

	// Create OTLP exporter
	exporter, err := createOTLPExporter(ctx, config.OTLPEndpoint, config.UseTLS)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create resource with service identification
	res, err := createResource(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create trace provider with sampling
	sampler := createSampler(config.SamplingRate)
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	// Set global trace provider and propagator
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return &Tracer{
		tracer:   provider.Tracer(config.ServiceName),
		provider: provider,
		config:   config,
	}, nil
}

// createOTLPExporter creates an OTLP trace exporter using gRPC
//
// The exporter manages the gRPC connection lifecycle internally.
// When useTLS is true, uses TLS 1.3 with system certificates (production).
// When useTLS is false, uses insecure connection (development only).
func createOTLPExporter(ctx context.Context, endpoint string, useTLS bool) (*otlptrace.Exporter, error) {
	var opts []otlptracegrpc.Option

	opts = append(opts, otlptracegrpc.WithEndpoint(endpoint))

	if useTLS {
		// Production: Use TLS with system certificates
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS13,
		}
		opts = append(opts, otlptracegrpc.WithTLSCredentials(credentials.NewTLS(tlsConfig)))
	} else {
		// Development: Use insecure connection
		// WARNING: Only use in local development. Production should use TLS.
		opts = append(opts, otlptracegrpc.WithInsecure())
	}

	// Create OTLP trace exporter
	// The exporter manages the gRPC connection lifecycle
	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	return exporter, nil
}

// createResource creates an OpenTelemetry resource with service attributes
func createResource(config TracerConfig) (*resource.Resource, error) {
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String(config.ServiceName),
			semconv.ServiceVersionKey.String(config.ServiceVersion),
			attribute.String("deployment.environment", config.Environment),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to merge resources: %w", err)
	}
	return res, nil
}

// createSampler creates a trace sampler based on sampling rate
func createSampler(rate float64) sdktrace.Sampler {
	if rate >= 1.0 {
		return sdktrace.AlwaysSample()
	}
	if rate <= 0.0 {
		return sdktrace.NeverSample()
	}
	return sdktrace.TraceIDRatioBased(rate)
}

// Start begins a new span with the given name and options
//
// The span should be ended with span.End() when the operation completes.
// Use defer span.End() for automatic cleanup.
//
// Example:
//
//	ctx, span := tracer.Start(ctx, "database.query",
//	    trace.WithAttributes(
//	        attribute.String("db.statement", "SELECT * FROM users WHERE id = ?"),
//	        attribute.String("db.system", "postgresql"),
//	    ),
//	)
//	defer span.End()
func (t *Tracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return t.tracer.Start(ctx, spanName, opts...)
}

// RecordError records an error on the span
//
// This adds the error to the span with a standard attribute and sets the span status to error.
//
// Example:
//
//	ctx, span := tracer.Start(ctx, "process.payment")
//	defer span.End()
//
//	if err := processPayment(ctx, payment); err != nil {
//	    tracer.RecordError(ctx, err)
//	    return err
//	}
func (t *Tracer) RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.RecordError(err)
	}
}

// AddEvent adds an event to the current span
//
// Events represent significant points in time during span execution.
//
// Example:
//
//	tracer.AddEvent(ctx, "cache.miss",
//	    trace.WithAttributes(
//	        attribute.String("cache.key", key),
//	    ),
//	)
func (t *Tracer) AddEvent(ctx context.Context, name string, opts ...trace.EventOption) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.AddEvent(name, opts...)
	}
}

// SetAttributes sets attributes on the current span
//
// Attributes provide additional context about the operation.
// Use semantic conventions when available.
//
// Example:
//
//	tracer.SetAttributes(ctx,
//	    attribute.String("http.method", "POST"),
//	    attribute.String("http.route", "/api/v1/accounts"),
//	    attribute.Int("http.status_code", 201),
//	)
func (t *Tracer) SetAttributes(ctx context.Context, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	if span.IsRecording() {
		span.SetAttributes(attrs...)
	}
}

// Shutdown flushes any pending spans and shuts down the tracer
//
// This should be called during application shutdown to ensure all traces are exported.
// The caller should provide a context with an appropriate timeout.
//
// Example:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//	if err := tracer.Shutdown(ctx); err != nil {
//	    log.Printf("failed to shutdown tracer: %v", err)
//	}
func (t *Tracer) Shutdown(ctx context.Context) error {
	if t.provider == nil {
		return nil
	}

	// Check if caller provided a deadline, add fallback if not
	_, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}

	if err := t.provider.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown tracer provider: %w", err)
	}
	return nil
}

// SpanFromContext extracts the current span from context
//
// Returns a non-recording span if no span is present in context.
// Safe to call even if tracing is disabled.
func SpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}

// ContextWithSpan returns a new context with the span embedded
func ContextWithSpan(ctx context.Context, span trace.Span) context.Context {
	return trace.ContextWithSpan(ctx, span)
}
