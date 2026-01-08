package kafka

import (
	"context"
	"errors"
	"testing"

	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNewProtoConsumer(t *testing.T) {
	msgFactory := func() proto.Message {
		return &timestamppb.Timestamp{}
	}
	handler := func(_ context.Context, _ []byte, _ proto.Message) error {
		return nil
	}

	tests := []struct {
		name       string
		config     ConsumerConfig
		msgFactory func() proto.Message
		handler    MessageHandler
		wantErr    bool
	}{
		{
			name: "valid config",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
				ClientID:         "test-consumer",
			},
			msgFactory: msgFactory,
			handler:    handler,
			wantErr:    false,
		},
		{
			name: "valid config with defaults",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
			},
			msgFactory: msgFactory,
			handler:    handler,
			wantErr:    false,
		},
		{
			name: "missing bootstrap servers",
			config: ConsumerConfig{
				GroupID: "test-group",
			},
			msgFactory: msgFactory,
			handler:    handler,
			wantErr:    true,
		},
		{
			name: "missing group ID",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
			},
			msgFactory: msgFactory,
			handler:    handler,
			wantErr:    true,
		},
		{
			name: "nil message factory",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
			},
			msgFactory: nil,
			handler:    handler,
			wantErr:    true,
		},
		{
			name: "nil handler",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test-group",
			},
			msgFactory: msgFactory,
			handler:    nil,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			consumer, err := NewProtoConsumer(tt.config, tt.msgFactory, tt.handler)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewProtoConsumer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && consumer != nil {
				defer func() {
					_ = consumer.Close()
				}()
			}
		})
	}
}

func TestProtoConsumer_Subscribe(t *testing.T) {
	msgFactory := func() proto.Message {
		return &timestamppb.Timestamp{}
	}
	handler := func(_ context.Context, _ []byte, _ proto.Message) error {
		return nil
	}

	consumer, err := NewProtoConsumer(ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
		ClientID:         "test-consumer",
	}, msgFactory, handler)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer func() {
		_ = consumer.Close()
	}()

	tests := []struct {
		name    string
		topics  []string
		wantErr bool
	}{
		{
			name:    "empty topics",
			topics:  []string{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := consumer.Subscribe(tt.topics)
			if (err != nil) != tt.wantErr {
				t.Errorf("Subscribe() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProtoConsumer_StopAndClose(t *testing.T) {
	msgFactory := func() proto.Message {
		return &timestamppb.Timestamp{}
	}
	handler := func(_ context.Context, _ []byte, _ proto.Message) error {
		return nil
	}

	consumer, err := NewProtoConsumer(ConsumerConfig{
		BootstrapServers: "localhost:9092",
		GroupID:          "test-group",
	}, msgFactory, handler)
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}

	// Stop should not panic
	consumer.Stop()

	// Close should not panic
	err = consumer.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestExtractTenantHeader(t *testing.T) {
	t.Run("valid tenant header", func(t *testing.T) {
		record := &kgo.Record{
			Topic: "test-topic",
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
			},
		}

		orgID, err := ExtractTenantHeader(record)
		if err != nil {
			t.Errorf("ExtractTenantHeader() unexpected error: %v", err)
		}
		if orgID.String() != "acme_bank" {
			t.Errorf("ExtractTenantHeader() = %q, want %q", orgID.String(), "acme_bank")
		}
	})

	t.Run("missing tenant header", func(t *testing.T) {
		record := &kgo.Record{
			Topic:   "test-topic",
			Headers: []kgo.RecordHeader{},
		}

		orgID, err := ExtractTenantHeader(record)
		if !errors.Is(err, ErrMissingTenantHeader) {
			t.Errorf("ExtractTenantHeader() error = %v, want ErrMissingTenantHeader", err)
		}
		if orgID != "" {
			t.Errorf("ExtractTenantHeader() orgID = %q, want empty", orgID)
		}
	})

	t.Run("invalid tenant header format", func(t *testing.T) {
		record := &kgo.Record{
			Topic: "test-topic",
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("invalid-org-id!")},
			},
		}

		orgID, err := ExtractTenantHeader(record)
		if !errors.Is(err, tenant.ErrInvalidTenantID) {
			t.Errorf("ExtractTenantHeader() error = %v, want ErrInvalidTenantID", err)
		}
		if orgID != "" {
			t.Errorf("ExtractTenantHeader() orgID = %q, want empty", orgID)
		}
	})

	t.Run("tenant header with other headers", func(t *testing.T) {
		record := &kgo.Record{
			Topic: "test-topic",
			Headers: []kgo.RecordHeader{
				{Key: "correlation-id", Value: []byte("12345")},
				{Key: tenant.TenantIDKey, Value: []byte("motive_financial")},
				{Key: "trace-id", Value: []byte("abcdef")},
			},
		}

		orgID, err := ExtractTenantHeader(record)
		if err != nil {
			t.Errorf("ExtractTenantHeader() unexpected error: %v", err)
		}
		if orgID.String() != "motive_financial" {
			t.Errorf("ExtractTenantHeader() = %q, want %q", orgID.String(), "motive_financial")
		}
	})

	t.Run("nil headers", func(t *testing.T) {
		record := &kgo.Record{
			Topic:   "test-topic",
			Headers: nil,
		}

		orgID, err := ExtractTenantHeader(record)
		if !errors.Is(err, ErrMissingTenantHeader) {
			t.Errorf("ExtractTenantHeader() error = %v, want ErrMissingTenantHeader", err)
		}
		if orgID != "" {
			t.Errorf("ExtractTenantHeader() orgID = %q, want empty", orgID)
		}
	})

	t.Run("empty tenant header value", func(t *testing.T) {
		record := &kgo.Record{
			Topic: "test-topic",
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("")},
			},
		}

		orgID, err := ExtractTenantHeader(record)
		if !errors.Is(err, tenant.ErrInvalidTenantID) {
			t.Errorf("ExtractTenantHeader() error = %v, want ErrInvalidTenantID", err)
		}
		if orgID != "" {
			t.Errorf("ExtractTenantHeader() orgID = %q, want empty", orgID)
		}
	})

	t.Run("nil record", func(t *testing.T) {
		orgID, err := ExtractTenantHeader(nil)
		if !errors.Is(err, ErrMissingTenantHeader) {
			t.Errorf("ExtractTenantHeader() error = %v, want ErrMissingTenantHeader", err)
		}
		if orgID != "" {
			t.Errorf("ExtractTenantHeader() orgID = %q, want empty", orgID)
		}
	})
}
