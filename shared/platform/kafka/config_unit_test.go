package kafka

import (
	"context"
	"os"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestDefaultAuditTopicConfig(t *testing.T) {
	config := DefaultAuditTopicConfig()

	if config.EventsTopic != AuditEventsTopic {
		t.Errorf("EventsTopic: got %q, want %q", config.EventsTopic, AuditEventsTopic)
	}
	if config.DLQTopic != AuditEventsDLQTopic {
		t.Errorf("DLQTopic: got %q, want %q", config.DLQTopic, AuditEventsDLQTopic)
	}
	if config.ConsumerGroup != AuditConsumerGroup {
		t.Errorf("ConsumerGroup: got %q, want %q", config.ConsumerGroup, AuditConsumerGroup)
	}
	if config.RetentionDays != DefaultAuditRetentionDays {
		t.Errorf("RetentionDays: got %d, want %d", config.RetentionDays, DefaultAuditRetentionDays)
	}
	if config.Partitions != 6 {
		t.Errorf("Partitions: got %d, want 6", config.Partitions)
	}
	if config.ReplicationFactor != 3 {
		t.Errorf("ReplicationFactor: got %d, want 3", config.ReplicationFactor)
	}
}

func TestAuditTopicConfigFromEnv(t *testing.T) {
	t.Run("defaults when env not set", func(t *testing.T) {
		// Clear env vars
		os.Unsetenv("AUDIT_KAFKA_TOPIC")
		os.Unsetenv("AUDIT_KAFKA_DLQ_TOPIC")
		os.Unsetenv("AUDIT_KAFKA_CONSUMER_GROUP")

		config := AuditTopicConfigFromEnv()
		if config.EventsTopic != AuditEventsTopic {
			t.Errorf("EventsTopic: got %q, want default", config.EventsTopic)
		}
		if config.DLQTopic != AuditEventsDLQTopic {
			t.Errorf("DLQTopic: got %q, want default", config.DLQTopic)
		}
		if config.ConsumerGroup != AuditConsumerGroup {
			t.Errorf("ConsumerGroup: got %q, want default", config.ConsumerGroup)
		}
	})

	t.Run("custom env overrides", func(t *testing.T) {
		t.Setenv("AUDIT_KAFKA_TOPIC", "custom-audit-topic")
		t.Setenv("AUDIT_KAFKA_DLQ_TOPIC", "custom-dlq-topic")
		t.Setenv("AUDIT_KAFKA_CONSUMER_GROUP", "custom-group")

		config := AuditTopicConfigFromEnv()
		if config.EventsTopic != "custom-audit-topic" {
			t.Errorf("EventsTopic: got %q, want %q", config.EventsTopic, "custom-audit-topic")
		}
		if config.DLQTopic != "custom-dlq-topic" {
			t.Errorf("DLQTopic: got %q, want %q", config.DLQTopic, "custom-dlq-topic")
		}
		if config.ConsumerGroup != "custom-group" {
			t.Errorf("ConsumerGroup: got %q, want %q", config.ConsumerGroup, "custom-group")
		}
	})
}

func TestSplitString(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		sep      rune
		expected []string
	}{
		{"single item", "localhost:9092", ',', []string{"localhost:9092"}},
		{"multiple items", "a,b,c", ',', []string{"a", "b", "c"}},
		{"empty string", "", ',', nil},
		{"trailing separator", "a,b,", ',', []string{"a", "b"}},
		{"leading separator", ",a,b", ',', []string{"", "a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitString(tt.input, tt.sep)
			if len(result) != len(tt.expected) {
				t.Errorf("splitString(%q, %q) = %v (len %d), want %v (len %d)",
					tt.input, string(tt.sep), result, len(result), tt.expected, len(tt.expected))
				return
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("splitString(%q, %q)[%d] = %q, want %q", tt.input, string(tt.sep), i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestSplitBrokers(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{"single broker", "localhost:9092", []string{"localhost:9092"}},
		{"multiple brokers", "broker1:9092,broker2:9092,broker3:9092", []string{"broker1:9092", "broker2:9092", "broker3:9092"}},
		{"empty string", "", nil},
		{"trailing comma filters empty", "a,b,", []string{"a", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitBrokers(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("splitBrokers(%q) = %v (len %d), want %v (len %d)",
					tt.input, result, len(result), tt.expected, len(tt.expected))
				return
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("splitBrokers(%q)[%d] = %q, want %q", tt.input, i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestNewProtoProducer_ConfigOptions(t *testing.T) {
	tests := []struct {
		name    string
		config  ProducerConfig
		wantErr bool
	}{
		{
			name: "acks all",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				Acks:             "all",
			},
		},
		{
			name: "acks minus one",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				Acks:             "-1",
			},
		},
		{
			name: "acks leader rejected by idempotency",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				Acks:             "1",
			},
			wantErr: true,
		},
		{
			name: "acks none rejected by idempotency",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				Acks:             "0",
			},
			wantErr: true,
		},
		{
			name: "acks unknown defaults to all",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				Acks:             "unknown",
			},
		},
		{
			name: "compression gzip",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				Compression:      "gzip",
			},
		},
		{
			name: "compression lz4",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				Compression:      "lz4",
			},
		},
		{
			name: "compression zstd",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				Compression:      "zstd",
			},
		},
		{
			name: "compression none",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				Compression:      "none",
			},
		},
		{
			name: "compression unknown defaults to snappy",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				Compression:      "unknown-codec",
			},
		},
		{
			name: "with client ID",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				ClientID:         "my-producer",
			},
		},
		{
			name: "custom retries",
			config: ProducerConfig{
				BootstrapServers: "localhost:9092",
				Retries:          10,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			producer, err := NewProtoProducer(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("NewProtoProducer() error = %v", err)
			}
			defer producer.Close()
		})
	}
}

func TestNewProtoConsumer_ConfigOptions(t *testing.T) {
	msgFactory := func() proto.Message { return &timestamppb.Timestamp{} }
	handler := func(_ context.Context, _ []byte, _ proto.Message) error { return nil }

	tests := []struct {
		name   string
		config ConsumerConfig
	}{
		{
			name: "auto offset reset latest",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test",
				AutoOffsetReset:  "latest",
			},
		},
		{
			name: "auto offset reset unknown defaults to earliest",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test",
				AutoOffsetReset:  "unknown",
			},
		},
		{
			name: "with client ID",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test",
				ClientID:         "my-consumer",
			},
		},
		{
			name: "auto commit enabled",
			config: ConsumerConfig{
				BootstrapServers: "localhost:9092",
				GroupID:          "test",
				EnableAutoCommit: true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			consumer, err := NewProtoConsumer(tt.config, msgFactory, handler)
			if err != nil {
				t.Fatalf("NewProtoConsumer() error = %v", err)
			}
			defer consumer.Close()
		})
	}
}
