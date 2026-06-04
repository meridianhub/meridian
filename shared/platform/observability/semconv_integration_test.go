package observability

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.41.0"
)

// newTestTracerWithExporter creates a tracer with an in-memory exporter for testing
func newTestTracerWithExporter(t *testing.T) (*Tracer, *tracetest.InMemoryExporter) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(exporter),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	return &Tracer{
		tracer:   tp.Tracer("test-service"),
		provider: tp,
		config: TracerConfig{
			ServiceName: "test-service",
			Enabled:     true,
		},
	}, exporter
}

// TestSemconvV1_37_DatabaseAttributes verifies that database spans use the correct
// semantic convention attributes from v1.37.0
func TestSemconvV1_37_DatabaseAttributes(t *testing.T) {
	ctx := context.Background()
	tracer, exporter := newTestTracerWithExporter(t)
	defer func() { _ = tracer.Shutdown(ctx) }()

	// Create a database span
	_, span := StartDatabaseSpan(ctx, tracer, DatabaseSpanOptions{
		System:    "postgresql",
		Name:      "meridian",
		Statement: "SELECT * FROM accounts WHERE id = $1",
		Operation: "SELECT",
		Table:     "accounts",
	})
	span.End()

	// Get the exported span
	spans := exporter.GetSpans()
	require.Len(t, spans, 1, "Expected exactly one span to be exported")

	// Verify semconv v1.37.0 attributes are present
	attrs := spans[0].Attributes
	assertHasAttribute(t, attrs, semconv.DBSystemNameKey, "postgresql")
	assertHasAttribute(t, attrs, semconv.DBOperationNameKey, "SELECT")
	assertHasAttribute(t, attrs, semconv.DBNamespaceKey, "meridian")
	assertHasAttribute(t, attrs, semconv.DBQueryTextKey, "SELECT * FROM accounts WHERE id = $1")
	assertHasAttribute(t, attrs, semconv.DBCollectionNameKey, "accounts")
}

// TestSemconvV1_37_KafkaProducerAttributes verifies that Kafka producer spans use
// the correct semantic convention attributes from v1.37.0
func TestSemconvV1_37_KafkaProducerAttributes(t *testing.T) {
	ctx := context.Background()
	tracer, exporter := newTestTracerWithExporter(t)
	defer func() { _ = tracer.Shutdown(ctx) }()

	// Create a Kafka producer span
	_, span := StartKafkaProducerSpan(ctx, tracer, KafkaSpanOptions{
		Topic:     "account.events",
		Partition: 3,
		Key:       "account-123",
	})
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	// Verify semconv v1.37.0 attributes are present with correct values
	// Note: MessagingSystemKey.String("kafka") produces attribute "messaging.system" = "kafka"
	// This is the correct v1.37.0 API even though the attribute name is the same
	attrs := spans[0].Attributes
	assertHasAttribute(t, attrs, semconv.MessagingSystemKey, "kafka")
	assertHasAttribute(t, attrs, semconv.MessagingOperationTypeKey, "publish")
	assertHasAttribute(t, attrs, semconv.MessagingDestinationNameKey, "account.events")
	assertHasAttribute(t, attrs, semconv.MessagingDestinationPartitionIDKey, "3")
	assertHasAttribute(t, attrs, semconv.MessagingKafkaMessageKeyKey, "account-123")

	// In v1.37.0, MessagingDestinationPartitionIDKey replaced custom "kafka.partition" attribute
	assertNotHasAttributeKey(t, attrs, "kafka.partition")
}

// TestSemconvV1_37_KafkaConsumerAttributes verifies that Kafka consumer spans use
// the correct semantic convention attributes from v1.37.0
func TestSemconvV1_37_KafkaConsumerAttributes(t *testing.T) {
	ctx := context.Background()
	tracer, exporter := newTestTracerWithExporter(t)
	defer func() { _ = tracer.Shutdown(ctx) }()

	// Create a Kafka consumer span
	_, span := StartKafkaConsumerSpan(ctx, tracer, KafkaSpanOptions{
		Topic:     "account.events",
		Partition: 2,
		Offset:    12345,
		Key:       "account-456",
	})
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	// Verify semconv v1.37.0 attributes - CRITICAL: consumer must use Key-based approach
	// The Key-based approach (MessagingSystemKey.String("kafka")) is v1.37.0 compliant
	// vs using deprecated constants (MessagingSystemKafka, MessagingOperationTypeReceive)
	attrs := spans[0].Attributes
	assertHasAttribute(t, attrs, semconv.MessagingSystemKey, "kafka")
	assertHasAttribute(t, attrs, semconv.MessagingOperationTypeKey, "receive")
	assertHasAttribute(t, attrs, semconv.MessagingDestinationNameKey, "account.events")
	assertHasAttribute(t, attrs, semconv.MessagingDestinationPartitionIDKey, "2")
	assertHasAttributeInt64(t, attrs, semconv.MessagingKafkaOffsetKey, 12345)
	assertHasAttribute(t, attrs, semconv.MessagingKafkaMessageKeyKey, "account-456")

	// Verify custom partition attribute was replaced with standard MessagingDestinationPartitionIDKey
	assertNotHasAttributeKey(t, attrs, "kafka.partition")
}

// TestSemconvV1_37_HTTPClientAttributes verifies that HTTP client spans use
// the correct semantic convention attributes from v1.37.0
func TestSemconvV1_37_HTTPClientAttributes(t *testing.T) {
	ctx := context.Background()
	tracer, exporter := newTestTracerWithExporter(t)
	defer func() { _ = tracer.Shutdown(ctx) }()

	// Create an HTTP client span
	_, span := StartHTTPClientSpan(ctx, tracer, HTTPClientSpanOptions{
		Method: "POST",
		URL:    "https://api.example.com/accounts/123",
		Host:   "api.example.com",
		Route:  "/accounts/{id}",
	})
	span.End()

	spans := exporter.GetSpans()
	require.Len(t, spans, 1)

	// Verify semconv v1.37.0 attributes are present
	attrs := spans[0].Attributes
	assertHasAttribute(t, attrs, semconv.HTTPRequestMethodKey, "POST")
	assertHasAttribute(t, attrs, semconv.URLFullKey, "https://api.example.com/accounts/123")
	assertHasAttribute(t, attrs, semconv.ServerAddressKey, "api.example.com")
	assertHasAttribute(t, attrs, semconv.HTTPRouteKey, "/accounts/{id}")
}

// Helper function to assert an attribute exists with expected value
func assertHasAttribute(t *testing.T, attrs []attribute.KeyValue, key attribute.Key, expected string) {
	t.Helper()
	for _, attr := range attrs {
		if attr.Key == key {
			assert.Equal(t, expected, attr.Value.AsString(),
				"Attribute %s should have value %q", key, expected)
			return
		}
	}
	t.Errorf("Expected attribute %s not found in span", key)
}

// Helper function to assert an int64 attribute exists with expected value
func assertHasAttributeInt64(t *testing.T, attrs []attribute.KeyValue, key attribute.Key, expected int64) {
	t.Helper()
	for _, attr := range attrs {
		if attr.Key == key {
			assert.Equal(t, expected, attr.Value.AsInt64(),
				"Attribute %s should have value %d", key, expected)
			return
		}
	}
	t.Errorf("Expected attribute %s not found in span", key)
}

// Helper function to assert an attribute key does NOT exist
func assertNotHasAttributeKey(t *testing.T, attrs []attribute.KeyValue, keyStr string) {
	t.Helper()
	for _, attr := range attrs {
		if string(attr.Key) == keyStr {
			t.Errorf("Attribute key %q should not be present (found value: %v)",
				keyStr, attr.Value.AsInterface())
		}
	}
}
