package kafka

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNewProtoConsumer(t *testing.T) {
	msgFactory := func() proto.Message {
		return &timestamppb.Timestamp{}
	}
	handler := func(ctx context.Context, key []byte, msg proto.Message) error {
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
				defer consumer.Close()
			}
		})
	}
}

func TestProtoConsumer_Subscribe(t *testing.T) {
	msgFactory := func() proto.Message {
		return &timestamppb.Timestamp{}
	}
	handler := func(ctx context.Context, key []byte, msg proto.Message) error {
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
	defer consumer.Close()

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
	handler := func(ctx context.Context, key []byte, msg proto.Message) error {
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
