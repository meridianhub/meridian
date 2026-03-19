package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// createTestConsumer creates a ProtoConsumer with custom handler for unit testing.
// This bypasses Kafka by directly creating the struct.
func createTestConsumer(handler MessageHandler, dlqProducer *DLQProducer, dlqConfig *DLQConfig) *ProtoConsumer {
	ctx, cancel := context.WithCancel(context.Background())
	return &ProtoConsumer{
		client:         nil, // Not used in processMessage tests
		msgFactory:     func() proto.Message { return &timestamppb.Timestamp{} },
		handler:        handler,
		pollTimeout:    100 * time.Millisecond,
		handlerTimeout: 5 * time.Second,
		dlqProducer:    dlqProducer,
		dlqConfig:      dlqConfig,
		ctx:            ctx,
		cancel:         cancel,
	}
}

func TestProcessMessage_Success(t *testing.T) {
	var receivedKey []byte
	var receivedMsg proto.Message
	var receivedTenant tenant.TenantID

	handler := func(ctx context.Context, key []byte, msg proto.Message) error {
		receivedKey = key
		receivedMsg = msg
		tid, err := tenant.RequireFromContext(ctx)
		if err != nil {
			return err
		}
		receivedTenant = tid
		return nil
	}

	consumer := createTestConsumer(handler, nil, nil)
	defer consumer.cancel()

	// Create a valid protobuf message
	ts := timestamppb.Now()
	data, err := proto.Marshal(ts)
	if err != nil {
		t.Fatalf("failed to marshal test message: %v", err)
	}

	record := &kgo.Record{
		Topic: "test-topic",
		Key:   []byte("test-key"),
		Value: data,
		Headers: []kgo.RecordHeader{
			{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
		},
	}

	err = consumer.processMessage(record)
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}

	if string(receivedKey) != "test-key" {
		t.Errorf("key: got %q, want %q", receivedKey, "test-key")
	}
	if receivedMsg == nil {
		t.Error("message should not be nil")
	}
	if receivedTenant.String() != "acme_bank" {
		t.Errorf("tenant: got %q, want %q", receivedTenant, "acme_bank")
	}
}

func TestProcessMessage_MissingTenantHeader(t *testing.T) {
	handler := func(_ context.Context, _ []byte, _ proto.Message) error {
		return nil
	}
	consumer := createTestConsumer(handler, nil, nil)
	defer consumer.cancel()

	record := &kgo.Record{
		Topic: "test-topic",
		Key:   []byte("key"),
		Value: []byte("data"),
	}

	err := consumer.processMessage(record)
	if err == nil {
		t.Fatal("expected error for missing tenant header")
	}
	if !errors.Is(err, ErrMissingTenantHeader) {
		t.Errorf("expected ErrMissingTenantHeader in error chain, got: %v", err)
	}
}

func TestProcessMessage_InvalidProtobuf(t *testing.T) {
	handler := func(_ context.Context, _ []byte, _ proto.Message) error {
		return nil
	}
	consumer := createTestConsumer(handler, nil, nil)
	defer consumer.cancel()

	record := &kgo.Record{
		Topic: "test-topic",
		Key:   []byte("key"),
		Value: []byte("not-valid-protobuf\xff\xfe"),
		Headers: []kgo.RecordHeader{
			{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
		},
	}

	err := consumer.processMessage(record)
	if err == nil {
		t.Fatal("expected error for invalid protobuf")
	}
	// Error should mention unmarshal failure
	if err.Error() == "" {
		t.Error("expected non-empty error message")
	}
}

func TestProcessMessage_HandlerError(t *testing.T) {
	handlerErr := errors.New("handler failed")
	handler := func(_ context.Context, _ []byte, _ proto.Message) error {
		return handlerErr
	}
	consumer := createTestConsumer(handler, nil, nil)
	defer consumer.cancel()

	ts := timestamppb.Now()
	data, _ := proto.Marshal(ts)

	record := &kgo.Record{
		Topic: "test-topic",
		Key:   []byte("key"),
		Value: data,
		Headers: []kgo.RecordHeader{
			{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
		},
	}

	err := consumer.processMessage(record)
	if err == nil {
		t.Fatal("expected handler error to propagate")
	}
	if !errors.Is(err, handlerErr) {
		t.Errorf("expected handler error in chain, got: %v", err)
	}
}

func TestProcessMessageWithRetry_NoDLQ(t *testing.T) {
	handlerErr := errors.New("processing failed")
	handler := func(_ context.Context, _ []byte, _ proto.Message) error {
		return handlerErr
	}
	consumer := createTestConsumer(handler, nil, nil)
	defer consumer.cancel()

	ts := timestamppb.Now()
	data, _ := proto.Marshal(ts)

	record := &kgo.Record{
		Topic: "test-topic",
		Key:   []byte("key"),
		Value: data,
		Headers: []kgo.RecordHeader{
			{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
		},
	}

	// Without DLQ, should attempt once and return error
	err := consumer.processMessageWithRetry(record)
	if err == nil {
		t.Fatal("expected error when DLQ not configured")
	}
}

func TestProcessMessageWithRetry_ShutdownDuringBackoff(t *testing.T) {
	callCount := 0
	handler := func(_ context.Context, _ []byte, _ proto.Message) error {
		callCount++
		return errors.New("always fails")
	}

	dlqConfig := &DLQConfig{
		MaxRetries:        5,
		RetryBackoffMs:    10000, // Long backoff
		BackoffMultiplier: 1.0,
		ConsumerGroupID:   "test",
	}

	// We can't create a real DLQ producer without Kafka, but we need non-nil
	// to trigger the retry path. Create consumer directly.
	ctx, cancel := context.WithCancel(context.Background())
	consumer := &ProtoConsumer{
		msgFactory:     func() proto.Message { return &timestamppb.Timestamp{} },
		handler:        handler,
		pollTimeout:    100 * time.Millisecond,
		handlerTimeout: 5 * time.Second,
		dlqProducer:    &DLQProducer{}, // Non-nil to trigger retry path
		dlqConfig:      dlqConfig,
		ctx:            ctx,
		cancel:         cancel,
	}

	ts := timestamppb.Now()
	data, _ := proto.Marshal(ts)
	record := &kgo.Record{
		Topic: "test-topic",
		Key:   []byte("key"),
		Value: data,
		Headers: []kgo.RecordHeader{
			{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
		},
	}

	// Cancel context during retry backoff
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()

	err := consumer.processMessageWithRetry(record)
	if err == nil {
		t.Fatal("expected error from shutdown during backoff")
	}

	// Should have attempted at least once before shutdown
	if callCount < 1 {
		t.Errorf("expected at least 1 attempt, got %d", callCount)
	}
}
