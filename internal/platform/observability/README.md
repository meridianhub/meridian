# Observability Package

This package provides distributed tracing functionality using OpenTelemetry for the Meridian platform.

## Features

- OpenTelemetry tracing with OTLP exporter
- Automatic gRPC tracing interceptors (client and server)
- Context propagation across service boundaries (gRPC, HTTP, Kafka)
- Configurable sampling strategies
- Semantic conventions for common operations (database, Kafka, HTTP)
- Integration with Grafana observability stack

## Quick Start

### 1. Initialize Tracer

```go
import "github.com/meridianhub/meridian/internal/platform/observability"

// Load configuration from environment
cfg, err := observability.DefaultConfig()
if err != nil {
    return fmt.Errorf("failed to load tracer config: %w", err)
}

// Create tracer
tracer, err := observability.NewTracer(ctx, cfg)
if err != nil {
    return fmt.Errorf("failed to initialize tracer: %w", err)
}
defer tracer.Shutdown(ctx)
```

### 2. Configure gRPC Server

```go
import (
    "google.golang.org/grpc"
    "github.com/meridianhub/meridian/internal/platform/observability"
)

grpcServer := grpc.NewServer(
    grpc.UnaryInterceptor(tracer.UnaryServerInterceptor()),
    grpc.StreamInterceptor(tracer.StreamServerInterceptor()),
)
```

### 3. Trace Business Operations

```go
// Start a span
ctx, span := tracer.Start(ctx, "account.create")
defer span.End()

// Add attributes
tracer.SetAttributes(ctx,
    attribute.String("account.id", accountID),
    attribute.String("account.type", "savings"),
)

// Record errors
if err := createAccount(ctx, account); err != nil {
    tracer.RecordError(ctx, err)
    return err
}
```

## Configuration

Configuration is loaded from environment variables:

| Variable | Description | Default |
|----------|-------------|---------|
| `OTEL_SERVICE_NAME` | Service name (required) | - |
| `OTEL_SERVICE_VERSION` | Service version | `unknown` |
| `OTEL_ENVIRONMENT` | Environment (dev, staging, prod) | `development` |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | OTLP collector endpoint | `alloy:4317` |
| `OTEL_EXPORTER_OTLP_INSECURE` | Disable TLS for OTLP connection | `true` (dev), `false` (prod) |
| `OTEL_TRACES_SAMPLER_ARG` | Sampling rate (0.0-1.0) | `1.0` (dev), `0.1` (prod) |
| `OTEL_TRACES_ENABLED` | Enable tracing | `true` |

### Example Configuration

```bash
# Development
export OTEL_SERVICE_NAME=current-account-service
export OTEL_SERVICE_VERSION=1.0.0
export OTEL_ENVIRONMENT=development
export OTEL_EXPORTER_OTLP_INSECURE=true  # Insecure connection (dev only)
export OTEL_TRACES_SAMPLER_ARG=1.0  # Sample 100% of traces

# Production
export OTEL_SERVICE_NAME=current-account-service
export OTEL_SERVICE_VERSION=1.0.0
export OTEL_ENVIRONMENT=production
export OTEL_EXPORTER_OTLP_INSECURE=false  # TLS enabled (prod)
export OTEL_TRACES_SAMPLER_ARG=0.1  # Sample 10% of traces
```

## Usage Examples

### Database Operations

```go
ctx, span := observability.StartDatabaseSpan(ctx, tracer, observability.DatabaseSpanOptions{
    System:    "postgresql",
    Name:      "meridian",
    Statement: "SELECT * FROM accounts WHERE id = $1",
    Operation: "SELECT",
    Table:     "accounts",
})
defer span.End()

rows, err := db.QueryContext(ctx, query, id)
if err != nil {
    observability.RecordDatabaseError(span, err)
    return err
}
```

### Kafka Producer

```go
ctx, span := observability.StartKafkaProducerSpan(ctx, tracer, observability.KafkaSpanOptions{
    Topic:     "account.events",
    Partition: 0,
    Key:       accountID,
})
defer span.End()

// Inject trace context into Kafka headers
headers := make(map[string]string)
observability.MapInjectTraceContext(ctx, headers)

err := producer.Produce(ctx, msg)
if err != nil {
    span.RecordError(err)
    return err
}
```

### Kafka Consumer

```go
// Extract trace context from Kafka headers
headers := kafkaHeadersToMap(msg.Headers)
ctx := observability.MapExtractTraceContext(context.Background(), headers)

ctx, span := observability.StartKafkaConsumerSpan(ctx, tracer, observability.KafkaSpanOptions{
    Topic:     "account.events",
    Partition: 0,
    Offset:    12345,
    Key:       accountID,
})
defer span.End()

err := processMessage(ctx, msg)
if err != nil {
    span.RecordError(err)
    return err
}
```

### HTTP Client

```go
ctx, span := observability.StartHTTPClientSpan(ctx, tracer, observability.HTTPClientSpanOptions{
    Method: "GET",
    URL:    "https://api.example.com/accounts/123",
    Host:   "api.example.com",
    Route:  "/accounts/{id}",
})
defer span.End()

req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
observability.HTTPInjectTraceContext(ctx, req.Header)

resp, err := client.Do(req)
if err != nil {
    span.RecordError(err)
    return err
}

observability.RecordHTTPResponse(span, resp.StatusCode)
```

### Business Operations

```go
ctx, span := observability.BusinessOperationSpan(ctx, tracer, "account.activate",
    attribute.String("account.id", accountID),
    attribute.String("account.type", "savings"),
)
defer span.End()

err := activateAccount(ctx, accountID)
if err != nil {
    span.RecordError(err)
    return err
}
```

## Grafana Stack Integration

The observability package integrates with the Grafana stack deployed via Tilt:

- **Grafana Alloy**: Receives OTLP traces on `alloy:4317`
- **Grafana Tempo**: Stores and queries traces
- **Grafana Loki**: Aggregates logs
- **Prometheus**: Collects metrics
- **Grafana**: Visualizes traces, logs, and metrics

### Accessing the Stack

When running Tilt:

- Grafana: <http://localhost:3000>
- Prometheus: <http://localhost:9090>

### Viewing Traces in Grafana

1. Open Grafana at <http://localhost:3000>
2. Navigate to "Explore"
3. Select "Tempo" datasource
4. Search for traces by:
   - Service name
   - Operation name
   - Trace ID
   - Duration
   - Tags/attributes

## Architecture

```text
Application
    ↓
OpenTelemetry SDK (tracing.go)
    ↓
OTLP Exporter
    ↓
Grafana Alloy (OTLP Collector)
    ↓
Grafana Tempo (Trace Storage)
    ↓
Grafana (Visualization)
```

## Testing

Run tests:

```bash
go test ./internal/platform/observability/...
```

Run tests with coverage:

```bash
go test -cover ./internal/platform/observability/...
```

## Best Practices

1. **Always defer span.End()**: Ensures spans are closed even if errors occur
2. **Use semantic conventions**: Leverage provided helpers for standard operations
3. **Sample in production**: Use 0.1 (10%) sampling to reduce overhead
4. **Add meaningful attributes**: Include business context (IDs, types, status)
5. **Record errors**: Always call `tracer.RecordError(ctx, err)` on errors
6. **Propagate context**: Pass context through the call chain
7. **Use detached context for background tasks**: Prevents premature cancellation

## Performance Considerations

- Sampling reduces overhead in production (10% recommended)
- Spans are batched before export
- OTLP uses gRPC for efficient transport
- No-op tracer when disabled (zero overhead)

## Troubleshooting

### Traces not appearing in Grafana

1. Check Alloy is running: `kubectl get pods -l app=alloy`
2. Verify OTLP endpoint: `OTEL_EXPORTER_OTLP_ENDPOINT=alloy:4317`
3. Check Alloy logs: `kubectl logs -l app=alloy`
4. Verify Tempo is receiving traces: `kubectl logs -l app=tempo`

### High memory usage

- Reduce sampling rate: `OTEL_TRACES_SAMPLER_ARG=0.1`
- Check for span leaks (missing `defer span.End()`)

### gRPC connection errors

- Verify Alloy service is accessible
- Check network policies
- Ensure port 4317 is not blocked
