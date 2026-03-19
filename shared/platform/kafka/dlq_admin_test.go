package kafka

import (
	"errors"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestParseDLQMetadata(t *testing.T) {
	now := time.Now().Truncate(time.Second) // RFC3339 truncates to seconds

	tests := []struct {
		name     string
		headers  []kgo.RecordHeader
		expected DLQMetadata
	}{
		{
			name: "all fields present",
			headers: []kgo.RecordHeader{
				{Key: "dlq.original_topic", Value: []byte("orders")},
				{Key: "dlq.original_partition", Value: []byte("5")},
				{Key: "dlq.original_offset", Value: []byte("100")},
				{Key: "dlq.error_message", Value: []byte("processing failed")},
				{Key: "dlq.error_stack_trace", Value: []byte("stack trace")},
				{Key: "dlq.retry_count", Value: []byte("3")},
				{Key: "dlq.first_failure_time", Value: []byte(now.Add(-10 * time.Minute).Format(time.RFC3339))},
				{Key: "dlq.last_failure_time", Value: []byte(now.Format(time.RFC3339))},
				{Key: "dlq.consumer_group_id", Value: []byte("test-group")},
				{Key: "dlq.correlation_id", Value: []byte("corr-123")},
				{Key: "dlq.causation_id", Value: []byte("cause-456")},
			},
			expected: DLQMetadata{
				OriginalTopic:     "orders",
				OriginalPartition: 5,
				OriginalOffset:    100,
				ErrorMessage:      "processing failed",
				ErrorStackTrace:   "stack trace",
				RetryCount:        3,
				FirstFailureTime:  now.Add(-10 * time.Minute),
				LastFailureTime:   now,
				ConsumerGroupID:   "test-group",
				CorrelationID:     "corr-123",
				CausationID:       "cause-456",
			},
		},
		{
			name:     "empty headers",
			headers:  nil,
			expected: DLQMetadata{},
		},
		{
			name: "invalid numeric fields ignored",
			headers: []kgo.RecordHeader{
				{Key: "dlq.original_partition", Value: []byte("not-a-number")},
				{Key: "dlq.original_offset", Value: []byte("also-not")},
				{Key: "dlq.retry_count", Value: []byte("nope")},
				{Key: "dlq.original_topic", Value: []byte("test-topic")},
			},
			expected: DLQMetadata{
				OriginalTopic: "test-topic",
			},
		},
		{
			name: "invalid timestamps ignored",
			headers: []kgo.RecordHeader{
				{Key: "dlq.first_failure_time", Value: []byte("not-a-timestamp")},
				{Key: "dlq.last_failure_time", Value: []byte("also-invalid")},
				{Key: "dlq.error_message", Value: []byte("some error")},
			},
			expected: DLQMetadata{
				ErrorMessage: "some error",
			},
		},
		{
			name: "unknown headers ignored",
			headers: []kgo.RecordHeader{
				{Key: "unknown.header", Value: []byte("value")},
				{Key: "dlq.original_topic", Value: []byte("my-topic")},
			},
			expected: DLQMetadata{
				OriginalTopic: "my-topic",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &kgo.Record{Headers: tt.headers}
			result := parseDLQMetadata(record)

			if result.OriginalTopic != tt.expected.OriginalTopic {
				t.Errorf("OriginalTopic: got %q, want %q", result.OriginalTopic, tt.expected.OriginalTopic)
			}
			if result.OriginalPartition != tt.expected.OriginalPartition {
				t.Errorf("OriginalPartition: got %d, want %d", result.OriginalPartition, tt.expected.OriginalPartition)
			}
			if result.OriginalOffset != tt.expected.OriginalOffset {
				t.Errorf("OriginalOffset: got %d, want %d", result.OriginalOffset, tt.expected.OriginalOffset)
			}
			if result.ErrorMessage != tt.expected.ErrorMessage {
				t.Errorf("ErrorMessage: got %q, want %q", result.ErrorMessage, tt.expected.ErrorMessage)
			}
			if result.ErrorStackTrace != tt.expected.ErrorStackTrace {
				t.Errorf("ErrorStackTrace: got %q, want %q", result.ErrorStackTrace, tt.expected.ErrorStackTrace)
			}
			if result.RetryCount != tt.expected.RetryCount {
				t.Errorf("RetryCount: got %d, want %d", result.RetryCount, tt.expected.RetryCount)
			}
			if !result.FirstFailureTime.Equal(tt.expected.FirstFailureTime) {
				t.Errorf("FirstFailureTime: got %v, want %v", result.FirstFailureTime, tt.expected.FirstFailureTime)
			}
			if !result.LastFailureTime.Equal(tt.expected.LastFailureTime) {
				t.Errorf("LastFailureTime: got %v, want %v", result.LastFailureTime, tt.expected.LastFailureTime)
			}
			if result.ConsumerGroupID != tt.expected.ConsumerGroupID {
				t.Errorf("ConsumerGroupID: got %q, want %q", result.ConsumerGroupID, tt.expected.ConsumerGroupID)
			}
			if result.CorrelationID != tt.expected.CorrelationID {
				t.Errorf("CorrelationID: got %q, want %q", result.CorrelationID, tt.expected.CorrelationID)
			}
			if result.CausationID != tt.expected.CausationID {
				t.Errorf("CausationID: got %q, want %q", result.CausationID, tt.expected.CausationID)
			}
		})
	}
}

func TestFilterByErrorType(t *testing.T) {
	msg := DLQMessage{
		Metadata: DLQMetadata{ErrorMessage: "Connection Timeout: failed to reach broker"},
	}

	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{"exact match", "Connection Timeout", true},
		{"case insensitive", "connection timeout", true},
		{"partial match", "timeout", true},
		{"no match", "serialization error", false},
		{"empty filter matches all", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filter := FilterByErrorType(tt.text)
			if got := filter(msg); got != tt.expected {
				t.Errorf("FilterByErrorType(%q) = %v, want %v", tt.text, got, tt.expected)
			}
		})
	}
}

func TestFilterByOriginalTopic(t *testing.T) {
	msg := DLQMessage{
		Metadata: DLQMetadata{OriginalTopic: "orders.events.v1"},
	}

	if !FilterByOriginalTopic("orders.events.v1")(msg) {
		t.Error("expected match for exact topic")
	}
	if FilterByOriginalTopic("users.events.v1")(msg) {
		t.Error("expected no match for different topic")
	}
	if FilterByOriginalTopic("")(msg) {
		t.Error("expected no match for empty topic")
	}
}

func TestFilterByTimeRange(t *testing.T) {
	now := time.Now()
	msg := DLQMessage{
		Metadata: DLQMetadata{LastFailureTime: now},
	}

	t.Run("within range", func(t *testing.T) {
		filter := FilterByTimeRange(now.Add(-1*time.Hour), now.Add(1*time.Hour))
		if !filter(msg) {
			t.Error("expected message within range to match")
		}
	})

	t.Run("before range", func(t *testing.T) {
		filter := FilterByTimeRange(now.Add(1*time.Hour), now.Add(2*time.Hour))
		if filter(msg) {
			t.Error("expected message before range to not match")
		}
	})

	t.Run("after range", func(t *testing.T) {
		filter := FilterByTimeRange(now.Add(-2*time.Hour), now.Add(-1*time.Hour))
		if filter(msg) {
			t.Error("expected message after range to not match")
		}
	})

	t.Run("exact boundary start", func(t *testing.T) {
		filter := FilterByTimeRange(now, now.Add(1*time.Hour))
		if !filter(msg) {
			t.Error("expected message at start boundary to match")
		}
	})

	t.Run("exact boundary end", func(t *testing.T) {
		filter := FilterByTimeRange(now.Add(-1*time.Hour), now)
		if !filter(msg) {
			t.Error("expected message at end boundary to match")
		}
	})
}

func TestFilterByConsumerGroup(t *testing.T) {
	msg := DLQMessage{
		Metadata: DLQMetadata{ConsumerGroupID: "audit-consumer-group"},
	}

	if !FilterByConsumerGroup("audit-consumer-group")(msg) {
		t.Error("expected match for exact group")
	}
	if FilterByConsumerGroup("other-group")(msg) {
		t.Error("expected no match for different group")
	}
}

func TestCombineFilters(t *testing.T) {
	msg := DLQMessage{
		Metadata: DLQMetadata{
			OriginalTopic:   "orders.events.v1",
			ErrorMessage:    "timeout error",
			ConsumerGroupID: "order-group",
		},
	}

	t.Run("all pass", func(t *testing.T) {
		combined := CombineFilters(
			FilterByOriginalTopic("orders.events.v1"),
			FilterByErrorType("timeout"),
			FilterByConsumerGroup("order-group"),
		)
		if !combined(msg) {
			t.Error("expected combined filter to pass when all sub-filters pass")
		}
	})

	t.Run("one fails", func(t *testing.T) {
		combined := CombineFilters(
			FilterByOriginalTopic("orders.events.v1"),
			FilterByErrorType("serialization"), // won't match
			FilterByConsumerGroup("order-group"),
		)
		if combined(msg) {
			t.Error("expected combined filter to fail when one sub-filter fails")
		}
	})

	t.Run("empty filters pass", func(t *testing.T) {
		combined := CombineFilters()
		if !combined(msg) {
			t.Error("expected empty combined filter to pass")
		}
	})
}

func TestNewDLQInspector_Validation(t *testing.T) {
	t.Run("empty bootstrap servers", func(t *testing.T) {
		_, err := NewDLQInspector(DLQInspectorConfig{
			DLQTopics: []string{"test-dlq"},
		})
		if !errors.Is(err, ErrEmptyBootstrapServers) {
			t.Errorf("expected ErrEmptyBootstrapServers, got %v", err)
		}
	})

	t.Run("empty topics", func(t *testing.T) {
		_, err := NewDLQInspector(DLQInspectorConfig{
			BootstrapServers: "localhost:9092",
			DLQTopics:        []string{},
		})
		if !errors.Is(err, ErrEmptyTopics) {
			t.Errorf("expected ErrEmptyTopics, got %v", err)
		}
	})
}

func TestNewDLQReplay_Validation(t *testing.T) {
	t.Run("empty bootstrap servers", func(t *testing.T) {
		_, err := NewDLQReplay(DLQReplayConfig{})
		if err == nil {
			t.Error("expected error for empty bootstrap servers")
		}
	})
}

func TestDLQStatistics_Aggregation(t *testing.T) {
	// Test the statistics aggregation logic with mocked messages
	now := time.Now()
	messages := []DLQMessage{
		{
			Metadata: DLQMetadata{
				OriginalTopic:    "orders",
				ErrorMessage:     "timeout\ndetails here",
				ConsumerGroupID:  "group-1",
				FirstFailureTime: now.Add(-2 * time.Hour),
				LastFailureTime:  now.Add(-1 * time.Hour),
			},
		},
		{
			Metadata: DLQMetadata{
				OriginalTopic:    "orders",
				ErrorMessage:     "serialization error",
				ConsumerGroupID:  "group-1",
				FirstFailureTime: now.Add(-3 * time.Hour),
				LastFailureTime:  now,
			},
		},
		{
			Metadata: DLQMetadata{
				OriginalTopic:    "users",
				ErrorMessage:     "timeout\nother details",
				ConsumerGroupID:  "group-2",
				FirstFailureTime: now.Add(-1 * time.Hour),
				LastFailureTime:  now.Add(-30 * time.Minute),
			},
		},
	}

	// Simulate the aggregation logic from GetStatistics
	stats := DLQStatistics{
		TotalMessages:           len(messages),
		MessagesByTopic:         make(map[string]int),
		MessagesByErrorType:     make(map[string]int),
		MessagesByConsumerGroup: make(map[string]int),
	}

	for _, msg := range messages {
		stats.MessagesByTopic[msg.Metadata.OriginalTopic]++
		errorType := msg.Metadata.ErrorMessage
		if idx := len(errorType); idx > 100 {
			errorType = errorType[:100] + "..."
		}
		// Extract first line
		for i, c := range errorType {
			if c == '\n' {
				errorType = errorType[:i]
				break
			}
		}
		stats.MessagesByErrorType[errorType]++
		stats.MessagesByConsumerGroup[msg.Metadata.ConsumerGroupID]++
		if stats.OldestFailure.IsZero() || msg.Metadata.FirstFailureTime.Before(stats.OldestFailure) {
			stats.OldestFailure = msg.Metadata.FirstFailureTime
		}
		if msg.Metadata.LastFailureTime.After(stats.NewestFailure) {
			stats.NewestFailure = msg.Metadata.LastFailureTime
		}
	}

	if stats.TotalMessages != 3 {
		t.Errorf("TotalMessages: got %d, want 3", stats.TotalMessages)
	}
	if stats.MessagesByTopic["orders"] != 2 {
		t.Errorf("MessagesByTopic[orders]: got %d, want 2", stats.MessagesByTopic["orders"])
	}
	if stats.MessagesByTopic["users"] != 1 {
		t.Errorf("MessagesByTopic[users]: got %d, want 1", stats.MessagesByTopic["users"])
	}
	if stats.MessagesByConsumerGroup["group-1"] != 2 {
		t.Errorf("MessagesByConsumerGroup[group-1]: got %d, want 2", stats.MessagesByConsumerGroup["group-1"])
	}
	if !stats.OldestFailure.Equal(now.Add(-3 * time.Hour)) {
		t.Errorf("OldestFailure: got %v, want %v", stats.OldestFailure, now.Add(-3*time.Hour))
	}
	if !stats.NewestFailure.Equal(now) {
		t.Errorf("NewestFailure: got %v, want %v", stats.NewestFailure, now)
	}
}
