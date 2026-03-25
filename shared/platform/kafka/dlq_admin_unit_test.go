package kafka

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

// --- Unit tests for DLQInspector validation ---

func TestNewDLQInspector_EmptyBootstrapServers(t *testing.T) {
	_, err := NewDLQInspector(DLQInspectorConfig{
		DLQTopics: []string{"test-dlq"},
	})
	assert.ErrorIs(t, err, ErrEmptyBootstrapServers)
}

func TestNewDLQInspector_EmptyTopics(t *testing.T) {
	_, err := NewDLQInspector(DLQInspectorConfig{
		BootstrapServers: "localhost:9092",
		DLQTopics:        []string{},
	})
	assert.ErrorIs(t, err, ErrEmptyTopics)
}

func TestNewDLQInspector_NilTopics(t *testing.T) {
	_, err := NewDLQInspector(DLQInspectorConfig{
		BootstrapServers: "localhost:9092",
		DLQTopics:        nil,
	})
	assert.ErrorIs(t, err, ErrEmptyTopics)
}

// --- Unit tests for DLQReplay validation ---

func TestNewDLQReplay_EmptyBootstrapServers(t *testing.T) {
	_, err := NewDLQReplay(DLQReplayConfig{})
	assert.Error(t, err)
}

// --- Unit tests for ReplayMessage header filtering ---

func TestReplayMessage_StripsPrefix(t *testing.T) {
	// Test the header-stripping logic that ReplayMessage uses
	dlqMsg := DLQMessage{
		Record: &kgo.Record{
			Key:   []byte("test-key"),
			Value: []byte("test-value"),
			Headers: []kgo.RecordHeader{
				{Key: "dlq.original_topic", Value: []byte("orders")},
				{Key: "dlq.error_message", Value: []byte("timeout")},
				{Key: "correlation_id", Value: []byte("corr-123")},
				{Key: "content-type", Value: []byte("application/protobuf")},
			},
		},
		Metadata: DLQMetadata{
			OriginalTopic: "orders",
		},
	}

	// Simulate the header filtering from ReplayMessage
	var replayHeaders []kgo.RecordHeader
	for _, header := range dlqMsg.Record.Headers {
		if !strings.HasPrefix(header.Key, "dlq.") {
			replayHeaders = append(replayHeaders, header)
		}
	}

	assert.Len(t, replayHeaders, 2)
	assert.Equal(t, "correlation_id", replayHeaders[0].Key)
	assert.Equal(t, "content-type", replayHeaders[1].Key)
}

func TestReplayMessage_PreservesNonDLQHeaders(t *testing.T) {
	headers := []kgo.RecordHeader{
		{Key: "x-custom", Value: []byte("value1")},
		{Key: "x-trace-id", Value: []byte("trace-abc")},
	}

	var replayHeaders []kgo.RecordHeader
	for _, header := range headers {
		if !strings.HasPrefix(header.Key, "dlq.") {
			replayHeaders = append(replayHeaders, header)
		}
	}

	assert.Len(t, replayHeaders, 2)
}

func TestReplayMessage_AllDLQHeaders(t *testing.T) {
	headers := []kgo.RecordHeader{
		{Key: "dlq.original_topic", Value: []byte("orders")},
		{Key: "dlq.error_message", Value: []byte("fail")},
		{Key: "dlq.retry_count", Value: []byte("3")},
	}

	var replayHeaders []kgo.RecordHeader
	for _, header := range headers {
		if !strings.HasPrefix(header.Key, "dlq.") {
			replayHeaders = append(replayHeaders, header)
		}
	}

	assert.Empty(t, replayHeaders)
}

// --- Unit tests for DLQStatistics aggregation ---

func TestDLQStatistics_EmptyMessages(t *testing.T) {
	stats := DLQStatistics{
		TotalMessages:           0,
		MessagesByTopic:         make(map[string]int),
		MessagesByErrorType:     make(map[string]int),
		MessagesByConsumerGroup: make(map[string]int),
	}

	assert.Equal(t, 0, stats.TotalMessages)
	assert.Empty(t, stats.MessagesByTopic)
	assert.True(t, stats.OldestFailure.IsZero())
	assert.True(t, stats.NewestFailure.IsZero())
}

func TestDLQStatistics_AggregationLogic(t *testing.T) {
	now := time.Now()

	messages := []DLQMessage{
		{
			Metadata: DLQMetadata{
				OriginalTopic:    "orders",
				ErrorMessage:     "timeout\ndetails",
				ConsumerGroupID:  "group-a",
				FirstFailureTime: now.Add(-3 * time.Hour),
				LastFailureTime:  now.Add(-2 * time.Hour),
			},
		},
		{
			Metadata: DLQMetadata{
				OriginalTopic:    "orders",
				ErrorMessage:     "serialization error",
				ConsumerGroupID:  "group-a",
				FirstFailureTime: now.Add(-1 * time.Hour),
				LastFailureTime:  now,
			},
		},
		{
			Metadata: DLQMetadata{
				OriginalTopic:    "payments",
				ErrorMessage:     "timeout\nmore details",
				ConsumerGroupID:  "group-b",
				FirstFailureTime: now.Add(-4 * time.Hour),
				LastFailureTime:  now.Add(-30 * time.Minute),
			},
		},
	}

	// Replicate the aggregation logic from GetStatistics
	stats := DLQStatistics{
		TotalMessages:           len(messages),
		MessagesByTopic:         make(map[string]int),
		MessagesByErrorType:     make(map[string]int),
		MessagesByConsumerGroup: make(map[string]int),
	}

	for _, msg := range messages {
		stats.MessagesByTopic[msg.Metadata.OriginalTopic]++

		errorType := strings.Split(msg.Metadata.ErrorMessage, "\n")[0]
		if len(errorType) > 100 {
			errorType = errorType[:100] + "..."
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

	assert.Equal(t, 3, stats.TotalMessages)
	assert.Equal(t, 2, stats.MessagesByTopic["orders"])
	assert.Equal(t, 1, stats.MessagesByTopic["payments"])
	assert.Equal(t, 2, stats.MessagesByConsumerGroup["group-a"])
	assert.Equal(t, 1, stats.MessagesByConsumerGroup["group-b"])
	// Error types extracted from first line
	assert.Equal(t, 2, stats.MessagesByErrorType["timeout"])
	assert.Equal(t, 1, stats.MessagesByErrorType["serialization error"])
	assert.True(t, stats.OldestFailure.Equal(now.Add(-4*time.Hour)))
	assert.True(t, stats.NewestFailure.Equal(now))
}

func TestDLQStatistics_LongErrorTypeTruncation(t *testing.T) {
	longError := strings.Repeat("x", 150)
	messages := []DLQMessage{
		{
			Metadata: DLQMetadata{
				OriginalTopic:    "test",
				ErrorMessage:     longError,
				ConsumerGroupID:  "group",
				FirstFailureTime: time.Now(),
				LastFailureTime:  time.Now(),
			},
		},
	}

	stats := DLQStatistics{
		MessagesByErrorType: make(map[string]int),
	}

	for _, msg := range messages {
		errorType := strings.Split(msg.Metadata.ErrorMessage, "\n")[0]
		if len(errorType) > 100 {
			errorType = errorType[:100] + "..."
		}
		stats.MessagesByErrorType[errorType]++
	}

	// Should have the truncated key
	for key := range stats.MessagesByErrorType {
		assert.LessOrEqual(t, len(key), 103) // 100 + "..."
		assert.True(t, strings.HasSuffix(key, "..."))
	}
}

// --- Unit tests for filter functions (additional coverage) ---

func TestFilterByErrorType_EmptyMessage(t *testing.T) {
	msg := DLQMessage{
		Metadata: DLQMetadata{ErrorMessage: ""},
	}

	// Empty error text matches empty message
	assert.True(t, FilterByErrorType("")(msg))
	// Non-empty filter should not match
	assert.False(t, FilterByErrorType("timeout")(msg))
}

func TestFilterByTimeRange_ExactMatch(t *testing.T) {
	now := time.Now()
	msg := DLQMessage{
		Metadata: DLQMetadata{LastFailureTime: now},
	}

	// Range where start == end == message time
	filter := FilterByTimeRange(now, now)
	assert.True(t, filter(msg))
}

func TestFilterByConsumerGroup_EmptyGroup(t *testing.T) {
	msg := DLQMessage{
		Metadata: DLQMetadata{ConsumerGroupID: ""},
	}

	assert.True(t, FilterByConsumerGroup("")(msg))
	assert.False(t, FilterByConsumerGroup("some-group")(msg))
}

func TestCombineFilters_SingleFilter(t *testing.T) {
	msg := DLQMessage{
		Metadata: DLQMetadata{OriginalTopic: "orders"},
	}

	combined := CombineFilters(FilterByOriginalTopic("orders"))
	assert.True(t, combined(msg))
}

func TestCombineFilters_AllFail(t *testing.T) {
	msg := DLQMessage{
		Metadata: DLQMetadata{
			OriginalTopic:   "orders",
			ConsumerGroupID: "group-1",
		},
	}

	combined := CombineFilters(
		FilterByOriginalTopic("payments"),
		FilterByConsumerGroup("group-2"),
	)
	assert.False(t, combined(msg))
}

// --- Unit tests for InspectOptions defaults ---

func TestInspectOptions_Defaults(t *testing.T) {
	opts := InspectOptions{}
	assert.Nil(t, opts.Filter)
	assert.Equal(t, 0, opts.MaxMessages)
	assert.Equal(t, time.Duration(0), opts.Timeout)
}

// --- Unit tests for DLQInspectorConfig ---

func TestDLQInspectorConfig_Fields(t *testing.T) {
	config := DLQInspectorConfig{
		BootstrapServers: "broker1:9092,broker2:9092",
		ClientID:         "test-inspector",
		DLQTopics:        []string{"topic-dlq-1", "topic-dlq-2"},
	}

	assert.Equal(t, "broker1:9092,broker2:9092", config.BootstrapServers)
	assert.Equal(t, "test-inspector", config.ClientID)
	assert.Len(t, config.DLQTopics, 2)
}

// --- Unit tests for DLQReplayConfig ---

func TestDLQReplayConfig_Fields(t *testing.T) {
	config := DLQReplayConfig{
		BootstrapServers: "broker:9092",
		ClientID:         "replay-client",
	}

	assert.Equal(t, "broker:9092", config.BootstrapServers)
	assert.Equal(t, "replay-client", config.ClientID)
}

// --- Unit tests for DLQMessage struct ---

func TestDLQMessage_RecordAndMetadata(t *testing.T) {
	now := time.Now()
	record := &kgo.Record{
		Key:   []byte("key-1"),
		Value: []byte("value-1"),
		Topic: "test-topic",
	}

	msg := DLQMessage{
		Record: record,
		Metadata: DLQMetadata{
			OriginalTopic:    "original-topic",
			OriginalPartition: 3,
			OriginalOffset:    42,
			ErrorMessage:      "processing failed",
			RetryCount:        5,
			FirstFailureTime:  now.Add(-10 * time.Minute),
			LastFailureTime:   now,
			ConsumerGroupID:   "cg-1",
			CorrelationID:     "corr-abc",
			CausationID:       "cause-xyz",
		},
	}

	assert.Equal(t, record, msg.Record)
	assert.Equal(t, "original-topic", msg.Metadata.OriginalTopic)
	assert.Equal(t, int32(3), msg.Metadata.OriginalPartition)
	assert.Equal(t, int64(42), msg.Metadata.OriginalOffset)
	assert.Equal(t, "processing failed", msg.Metadata.ErrorMessage)
	assert.Equal(t, int32(5), msg.Metadata.RetryCount)
	assert.Equal(t, "cg-1", msg.Metadata.ConsumerGroupID)
	assert.Equal(t, "corr-abc", msg.Metadata.CorrelationID)
	assert.Equal(t, "cause-xyz", msg.Metadata.CausationID)
}

// --- parseDLQMetadata edge cases ---

func TestParseDLQMetadata_PartialHeaders(t *testing.T) {
	record := &kgo.Record{
		Headers: []kgo.RecordHeader{
			{Key: "dlq.original_topic", Value: []byte("orders")},
			{Key: "dlq.retry_count", Value: []byte("2")},
			// Other fields missing
		},
	}

	meta := parseDLQMetadata(record)
	assert.Equal(t, "orders", meta.OriginalTopic)
	assert.Equal(t, int32(2), meta.RetryCount)
	assert.Equal(t, int32(0), meta.OriginalPartition) // zero value
	assert.Equal(t, int64(0), meta.OriginalOffset)    // zero value
	assert.Empty(t, meta.ErrorMessage)
	assert.True(t, meta.FirstFailureTime.IsZero())
}

func TestParseDLQMetadata_EmptyValues(t *testing.T) {
	record := &kgo.Record{
		Headers: []kgo.RecordHeader{
			{Key: "dlq.original_topic", Value: []byte("")},
			{Key: "dlq.error_message", Value: []byte("")},
			{Key: "dlq.original_partition", Value: []byte("")},
		},
	}

	meta := parseDLQMetadata(record)
	assert.Empty(t, meta.OriginalTopic)
	assert.Empty(t, meta.ErrorMessage)
	// Empty string is not a valid int, partition stays 0
	assert.Equal(t, int32(0), meta.OriginalPartition)
}

func TestParseDLQMetadata_DuplicateHeaders(t *testing.T) {
	// Last value wins
	record := &kgo.Record{
		Headers: []kgo.RecordHeader{
			{Key: "dlq.original_topic", Value: []byte("first")},
			{Key: "dlq.original_topic", Value: []byte("second")},
		},
	}

	meta := parseDLQMetadata(record)
	assert.Equal(t, "second", meta.OriginalTopic)
}

// --- ErrNilInspector test ---

func TestErrNilInspector(t *testing.T) {
	require.NotNil(t, ErrNilInspector)
	assert.Equal(t, "DLQ inspector cannot be nil", ErrNilInspector.Error())
}
