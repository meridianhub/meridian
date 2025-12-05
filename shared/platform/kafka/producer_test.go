package kafka

import (
	"context"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNewProtoProducer(t *testing.T) {
	tests := []struct {
		name    string
		config  ProducerConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				ClientID:         "test-producer",
			},
			wantErr: false,
		},
		{
			name: "valid config with defaults",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
			},
			wantErr: false,
		},
		{
			name: "missing bootstrap servers",
			config: ProducerConfig{
				ClientID: "test-producer",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			producer, err := NewProtoProducer(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewProtoProducer() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && producer != nil {
				defer producer.Close()
			}
		})
	}
}

func TestProtoProducer_Publish(t *testing.T) {
	// Skip if Kafka is not available
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-producer",
	})
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer producer.Close()

	tests := []struct {
		name    string
		topic   string
		key     string
		msg     func() interface{}
		wantErr bool
	}{
		{
			name:  "empty topic",
			topic: "",
			key:   "test-key",
			msg: func() interface{} {
				return timestamppb.Now()
			},
			wantErr: true,
		},
		{
			name:  "nil message",
			topic: "test-topic",
			key:   "test-key",
			msg: func() interface{} {
				return nil
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			var msg interface{}
			if tt.msg != nil {
				msg = tt.msg()
			}

			var err error
			if msg == nil {
				err = producer.Publish(ctx, tt.topic, tt.key, nil)
			} else if protoMsg, ok := msg.(*timestamppb.Timestamp); ok {
				err = producer.Publish(ctx, tt.topic, tt.key, protoMsg)
			}

			if (err != nil) != tt.wantErr {
				t.Errorf("Publish() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestProtoProducer_Flush(t *testing.T) {
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-producer",
	})
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer producer.Close()

	// Test flush with timeout
	remaining := producer.Flush(1000)
	if remaining < 0 {
		t.Errorf("Flush() returned negative value: %d", remaining)
	}
}

func TestProtoProducer_Close(t *testing.T) {
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-producer",
	})
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}

	// Close should not panic
	producer.Close()
}
