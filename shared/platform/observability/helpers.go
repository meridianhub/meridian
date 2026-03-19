package observability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"
)

// DatabaseSpanOptions configures a database span
type DatabaseSpanOptions struct {
	System    string // "postgresql", "cockroachdb", "redis"
	Name      string // Database name
	Statement string // SQL query or command
	Operation string // "SELECT", "INSERT", "UPDATE", "DELETE", "GET", "SET"
	Table     string // Table name (optional)
}

// StartDatabaseSpan creates a span for a database operation
//
// This automatically adds standard database semantic conventions.
//
// Example:
//
//	ctx, span := observability.StartDatabaseSpan(ctx, tracer, observability.DatabaseSpanOptions{
//	    System:    "postgresql",
//	    Name:      "meridian",
//	    Statement: "SELECT * FROM accounts WHERE id = $1",
//	    Operation: "SELECT",
//	    Table:     "accounts",
//	})
//	defer span.End()
//
//	rows, err := db.QueryContext(ctx, query, id)
//	if err != nil {
//	    observability.RecordDatabaseError(span, err)
//	    return err
//	}
func StartDatabaseSpan(ctx context.Context, tracer *Tracer, opts DatabaseSpanOptions) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{
		semconv.DBSystemNameKey.String(opts.System),
		semconv.DBOperationNameKey.String(opts.Operation),
	}

	if opts.Name != "" {
		attrs = append(attrs, semconv.DBNamespaceKey.String(opts.Name))
	}

	if opts.Statement != "" {
		attrs = append(attrs, semconv.DBQueryTextKey.String(opts.Statement))
	}

	if opts.Table != "" {
		attrs = append(attrs, semconv.DBCollectionNameKey.String(opts.Table))
	}

	spanName := opts.Operation
	if opts.Table != "" {
		spanName = opts.Operation + " " + opts.Table
	}

	return tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
}

// RecordDatabaseError records a database error on the span
func RecordDatabaseError(span trace.Span, err error) {
	if err == nil {
		return
	}

	span.RecordError(err)

	if errors.Is(err, sql.ErrNoRows) {
		span.SetStatus(codes.Ok, "") // Not found is not an error
		span.SetAttributes(attribute.Bool("db.result.empty", true))
	} else {
		span.SetStatus(codes.Error, err.Error())
	}
}

// KafkaSpanOptions configures a Kafka span
type KafkaSpanOptions struct {
	Operation string // "publish", "receive", "process"
	Topic     string
	Partition int32
	Offset    int64
	Key       string
}

// StartKafkaProducerSpan creates a span for publishing to Kafka
//
// Example:
//
//	ctx, span := observability.StartKafkaProducerSpan(ctx, tracer, observability.KafkaSpanOptions{
//	    Topic:     "account.events",
//	    Partition: 0,
//	    Key:       accountID,
//	})
//	defer span.End()
//
//	err := producer.Produce(ctx, msg)
//	if err != nil {
//	    span.RecordError(err)
//	    return err
//	}
func StartKafkaProducerSpan(ctx context.Context, tracer *Tracer, opts KafkaSpanOptions) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{
		semconv.MessagingSystemKey.String("kafka"),
		semconv.MessagingOperationTypeKey.String("publish"),
		semconv.MessagingDestinationNameKey.String(opts.Topic),
	}

	if opts.Partition >= 0 {
		// Use standard messaging.destination.partition.id attribute (available in v1.37.0+)
		attrs = append(attrs, semconv.MessagingDestinationPartitionIDKey.String(fmt.Sprintf("%d", opts.Partition)))
	}

	if opts.Key != "" {
		attrs = append(attrs, semconv.MessagingKafkaMessageKeyKey.String(opts.Key))
	}

	return tracer.Start(ctx, opts.Topic+" publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attrs...),
	)
}

// StartKafkaConsumerSpan creates a span for consuming from Kafka
//
// Example:
//
//	ctx, span := observability.StartKafkaConsumerSpan(ctx, tracer, observability.KafkaSpanOptions{
//	    Topic:     "account.events",
//	    Partition: 0,
//	    Offset:    12345,
//	    Key:       accountID,
//	})
//	defer span.End()
//
//	err := processMessage(ctx, msg)
//	if err != nil {
//	    span.RecordError(err)
//	    return err
//	}
func StartKafkaConsumerSpan(ctx context.Context, tracer *Tracer, opts KafkaSpanOptions) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{
		semconv.MessagingSystemKey.String("kafka"),
		semconv.MessagingOperationTypeKey.String("receive"),
		semconv.MessagingDestinationNameKey.String(opts.Topic),
	}

	if opts.Partition >= 0 {
		// Use standard messaging.destination.partition.id attribute (available in v1.37.0+)
		attrs = append(attrs, semconv.MessagingDestinationPartitionIDKey.String(fmt.Sprintf("%d", opts.Partition)))
	}

	if opts.Offset >= 0 {
		attrs = append(attrs, semconv.MessagingKafkaOffsetKey.Int64(opts.Offset))
	}

	if opts.Key != "" {
		attrs = append(attrs, semconv.MessagingKafkaMessageKeyKey.String(opts.Key))
	}

	return tracer.Start(ctx, opts.Topic+" receive",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attrs...),
	)
}

// HTTPClientSpanOptions configures an HTTP client span
type HTTPClientSpanOptions struct {
	Method string // GET, POST, etc.
	URL    string
	Host   string
	Route  string // Route pattern (e.g., "/api/v1/accounts/{id}")
}

// StartHTTPClientSpan creates a span for outgoing HTTP requests
//
// Example:
//
//	ctx, span := observability.StartHTTPClientSpan(ctx, tracer, observability.HTTPClientSpanOptions{
//	    Method: "GET",
//	    URL:    "https://api.example.com/accounts/123",
//	    Host:   "api.example.com",
//	    Route:  "/accounts/{id}",
//	})
//	defer span.End()
//
//	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
//	resp, err := client.Do(req)
//	if err != nil {
//	    span.RecordError(err)
//	    return err
//	}
//	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
func StartHTTPClientSpan(ctx context.Context, tracer *Tracer, opts HTTPClientSpanOptions) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{
		semconv.HTTPRequestMethodKey.String(opts.Method),
		semconv.URLFullKey.String(opts.URL),
	}

	if opts.Host != "" {
		attrs = append(attrs, semconv.ServerAddressKey.String(opts.Host))
	}

	if opts.Route != "" {
		attrs = append(attrs, semconv.HTTPRouteKey.String(opts.Route))
	}

	spanName := opts.Method
	if opts.Route != "" {
		spanName = opts.Method + " " + opts.Route
	}

	return tracer.Start(ctx, spanName,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attrs...),
	)
}

// RecordHTTPResponse records HTTP response attributes on the span
func RecordHTTPResponse(span trace.Span, statusCode int) {
	span.SetAttributes(semconv.HTTPResponseStatusCodeKey.Int(statusCode))

	if statusCode >= 400 {
		span.SetStatus(codes.Error, "HTTP error")
	} else {
		span.SetStatus(codes.Ok, "")
	}
}

// BusinessOperationSpan creates a span for business logic operations
//
// This is useful for tracing domain-specific operations that don't fit
// standard semantic conventions.
//
// Example:
//
//	ctx, span := observability.BusinessOperationSpan(ctx, tracer, "account.activate",
//	    attribute.String("account.id", accountID),
//	    attribute.String("account.type", "savings"),
//	)
//	defer span.End()
//
//	err := activateAccount(ctx, accountID)
//	if err != nil {
//	    span.RecordError(err)
//	    return err
//	}
func BusinessOperationSpan(ctx context.Context, tracer *Tracer, operation string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return tracer.Start(ctx, operation,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(attrs...),
	)
}
