package kafka

import (
	"context"
	"errors"
	"testing"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"github.com/meridianhub/meridian/shared/platform/organization"
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

func TestExtractOrganizationHeader(t *testing.T) {
	topic := "test-topic"

	t.Run("valid organization header", func(t *testing.T) {
		msg := &kafka.Message{
			TopicPartition: kafka.TopicPartition{Topic: &topic},
			Headers: []kafka.Header{
				{Key: organization.OrgIDKey, Value: []byte("acme_bank")},
			},
		}

		orgID, err := ExtractOrganizationHeader(msg)
		if err != nil {
			t.Errorf("ExtractOrganizationHeader() unexpected error: %v", err)
		}
		if orgID.String() != "acme_bank" {
			t.Errorf("ExtractOrganizationHeader() = %q, want %q", orgID.String(), "acme_bank")
		}
	})

	t.Run("missing organization header", func(t *testing.T) {
		msg := &kafka.Message{
			TopicPartition: kafka.TopicPartition{Topic: &topic},
			Headers:        []kafka.Header{},
		}

		orgID, err := ExtractOrganizationHeader(msg)
		if !errors.Is(err, ErrMissingOrganizationHeader) {
			t.Errorf("ExtractOrganizationHeader() error = %v, want ErrMissingOrganizationHeader", err)
		}
		if orgID != "" {
			t.Errorf("ExtractOrganizationHeader() orgID = %q, want empty", orgID)
		}
	})

	t.Run("invalid organization header format", func(t *testing.T) {
		msg := &kafka.Message{
			TopicPartition: kafka.TopicPartition{Topic: &topic},
			Headers: []kafka.Header{
				{Key: organization.OrgIDKey, Value: []byte("invalid-org-id!")},
			},
		}

		orgID, err := ExtractOrganizationHeader(msg)
		if !errors.Is(err, organization.ErrInvalidOrganizationID) {
			t.Errorf("ExtractOrganizationHeader() error = %v, want ErrInvalidOrganizationID", err)
		}
		if orgID != "" {
			t.Errorf("ExtractOrganizationHeader() orgID = %q, want empty", orgID)
		}
	})

	t.Run("organization header with other headers", func(t *testing.T) {
		msg := &kafka.Message{
			TopicPartition: kafka.TopicPartition{Topic: &topic},
			Headers: []kafka.Header{
				{Key: "correlation-id", Value: []byte("12345")},
				{Key: organization.OrgIDKey, Value: []byte("motive_financial")},
				{Key: "trace-id", Value: []byte("abcdef")},
			},
		}

		orgID, err := ExtractOrganizationHeader(msg)
		if err != nil {
			t.Errorf("ExtractOrganizationHeader() unexpected error: %v", err)
		}
		if orgID.String() != "motive_financial" {
			t.Errorf("ExtractOrganizationHeader() = %q, want %q", orgID.String(), "motive_financial")
		}
	})

	t.Run("nil headers", func(t *testing.T) {
		msg := &kafka.Message{
			TopicPartition: kafka.TopicPartition{Topic: &topic},
			Headers:        nil,
		}

		orgID, err := ExtractOrganizationHeader(msg)
		if !errors.Is(err, ErrMissingOrganizationHeader) {
			t.Errorf("ExtractOrganizationHeader() error = %v, want ErrMissingOrganizationHeader", err)
		}
		if orgID != "" {
			t.Errorf("ExtractOrganizationHeader() orgID = %q, want empty", orgID)
		}
	})

	t.Run("empty organization header value", func(t *testing.T) {
		msg := &kafka.Message{
			TopicPartition: kafka.TopicPartition{Topic: &topic},
			Headers: []kafka.Header{
				{Key: organization.OrgIDKey, Value: []byte("")},
			},
		}

		orgID, err := ExtractOrganizationHeader(msg)
		if !errors.Is(err, organization.ErrInvalidOrganizationID) {
			t.Errorf("ExtractOrganizationHeader() error = %v, want ErrInvalidOrganizationID", err)
		}
		if orgID != "" {
			t.Errorf("ExtractOrganizationHeader() orgID = %q, want empty", orgID)
		}
	})

	t.Run("nil message", func(t *testing.T) {
		orgID, err := ExtractOrganizationHeader(nil)
		if !errors.Is(err, ErrMissingOrganizationHeader) {
			t.Errorf("ExtractOrganizationHeader() error = %v, want ErrMissingOrganizationHeader", err)
		}
		if orgID != "" {
			t.Errorf("ExtractOrganizationHeader() orgID = %q, want empty", orgID)
		}
	})
}
