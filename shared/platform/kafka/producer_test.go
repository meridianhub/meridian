package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/meridianhub/meridian/shared/platform/tenant"
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

func TestProtoProducer_PublishWithTenant(t *testing.T) {
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-producer",
	})
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer producer.Close()

	t.Run("empty topic returns error", func(t *testing.T) {
		orgID := tenant.MustNewTenantID("acme_bank")
		ctx := tenant.WithTenant(context.Background(), orgID)
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		err := producer.PublishWithTenant(ctx, "", "test-key", timestamppb.Now())
		if !errors.Is(err, ErrEmptyTopic) {
			t.Errorf("PublishWithTenant() error = %v, want ErrEmptyTopic", err)
		}
	})

	t.Run("nil message returns error", func(t *testing.T) {
		orgID := tenant.MustNewTenantID("acme_bank")
		ctx := tenant.WithTenant(context.Background(), orgID)
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		err := producer.PublishWithTenant(ctx, "test-topic", "test-key", nil)
		if !errors.Is(err, ErrNilMessage) {
			t.Errorf("PublishWithTenant() error = %v, want ErrNilMessage", err)
		}
	})
}

func TestProtoProducer_PublishWithTenant_MissingContext(t *testing.T) {
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-producer",
	})
	if err != nil {
		t.Skip("Kafka not available, skipping integration test")
	}
	defer producer.Close()

	// Test that missing tenant context causes panic (fail-fast)
	defer func() {
		if r := recover(); r == nil {
			t.Error("PublishWithTenant did not panic for context without tenant")
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// This should panic because tenant context is missing
	_ = producer.PublishWithTenant(ctx, "test-topic", "test-key", timestamppb.Now())
}
