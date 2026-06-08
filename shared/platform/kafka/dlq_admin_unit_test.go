package kafka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	assert.ErrorIs(t, err, ErrEmptyBootstrapServers)
}

// Note: ReplayMessage, ReplayMessages, and GetStatistics require a live Kafka
// broker and are covered by integration tests. Unit tests here focus on the
// pure functions (parseDLQMetadata, filter combinators, config validation)
// that can be tested without external dependencies.

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

// --- Round-trip test: ToRecordHeaders -> parseDLQMetadata ---

func TestDLQMetadata_RoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second) // RFC3339 truncates to seconds
	original := DLQMetadata{
		OriginalTopic:     "orders.events.v1",
		OriginalPartition: 7,
		OriginalOffset:    12345,
		ErrorMessage:      "processing failed: timeout",
		ErrorStackTrace:   "goroutine 1 [running]:\nmain.go:42",
		RetryCount:        3,
		FirstFailureTime:  now.Add(-10 * time.Minute),
		LastFailureTime:   now,
		ConsumerGroupID:   "order-processor",
		CorrelationID:     "corr-abc-123",
		CausationID:       "cause-xyz-456",
	}

	// Serialize to headers, then parse back
	headers := original.ToRecordHeaders()
	record := &kgo.Record{Headers: headers}
	parsed := parseDLQMetadata(record)

	assert.Equal(t, original.OriginalTopic, parsed.OriginalTopic)
	assert.Equal(t, original.OriginalPartition, parsed.OriginalPartition)
	assert.Equal(t, original.OriginalOffset, parsed.OriginalOffset)
	assert.Equal(t, original.ErrorMessage, parsed.ErrorMessage)
	assert.Equal(t, original.ErrorStackTrace, parsed.ErrorStackTrace)
	assert.Equal(t, original.RetryCount, parsed.RetryCount)
	assert.True(t, original.FirstFailureTime.Equal(parsed.FirstFailureTime))
	assert.True(t, original.LastFailureTime.Equal(parsed.LastFailureTime))
	assert.Equal(t, original.ConsumerGroupID, parsed.ConsumerGroupID)
	assert.Equal(t, original.CorrelationID, parsed.CorrelationID)
	assert.Equal(t, original.CausationID, parsed.CausationID)
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

// --- checkFetchErrors: pure error-classification logic (no broker) ---
//
// kgo.NewErrFetch synthesizes a Fetches carrying a single error, letting us
// exercise every branch of checkFetchErrors without a live broker.

func TestCheckFetchErrors(t *testing.T) {
	realErr := errors.New("partition unavailable")

	tests := []struct {
		name      string
		fetches   kgo.Fetches
		wantErr   bool
		wantWraps error
	}{
		{
			name:    "no errors returns nil",
			fetches: kgo.Fetches{},
			wantErr: false,
		},
		{
			name:    "deadline exceeded is ignored",
			fetches: kgo.NewErrFetch(context.DeadlineExceeded),
			wantErr: false,
		},
		{
			name:    "context canceled is ignored",
			fetches: kgo.NewErrFetch(context.Canceled),
			wantErr: false,
		},
		{
			name:      "real error is surfaced and wrapped",
			fetches:   kgo.NewErrFetch(realErr),
			wantErr:   true,
			wantWraps: realErr,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkFetchErrors(tt.fetches)
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), "error reading DLQ message")
			if tt.wantWraps != nil {
				assert.ErrorIs(t, err, tt.wantWraps)
			}
		})
	}
}

// --- Constructor success paths + Close (lazy client, no broker contact) ---
//
// kgo.NewClient connects lazily, so the inspector/replay constructors succeed
// with an unreachable broker address. Close tears down the lazy client without
// any network round-trip.

func TestNewDLQInspector_Success(t *testing.T) {
	inspector, err := NewDLQInspector(DLQInspectorConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-inspector",
		DLQTopics:        []string{"orders.dlq", "users.dlq"},
	})
	require.NoError(t, err)
	require.NotNil(t, inspector)
	assert.NotNil(t, inspector.client)
	assert.NotNil(t, inspector.admin)
	assert.Equal(t, []string{"orders.dlq", "users.dlq"}, inspector.config.DLQTopics)

	assert.NoError(t, inspector.Close())
}

func TestNewDLQInspector_Success_NoClientID(t *testing.T) {
	inspector, err := NewDLQInspector(DLQInspectorConfig{
		BootstrapServers: "localhost:9092",
		DLQTopics:        []string{"orders.dlq"},
	})
	require.NoError(t, err)
	require.NotNil(t, inspector)

	assert.NoError(t, inspector.Close())
}

func TestNewDLQReplay_Success(t *testing.T) {
	replay, err := NewDLQReplay(DLQReplayConfig{
		BootstrapServers: "localhost:9092",
		ClientID:         "test-replay",
	})
	require.NoError(t, err)
	require.NotNil(t, replay)
	assert.NotNil(t, replay.producer)
	assert.Equal(t, "localhost:9092", replay.config.BootstrapServers)

	replay.Close()
}

// --- ReplayProtoMessage: unmarshal failure short-circuits before producing ---
//
// When the DLQ payload is not valid protobuf, ReplayProtoMessage returns the
// unmarshal error before touching the producer, so no broker is required.

func TestReplayProtoMessage_UnmarshalError(t *testing.T) {
	replay, err := NewDLQReplay(DLQReplayConfig{
		BootstrapServers: "localhost:9092",
	})
	require.NoError(t, err)
	defer replay.Close()

	dlqMsg := DLQMessage{
		Record: &kgo.Record{
			Value: []byte{0xFF, 0xFF, 0xFF}, // invalid protobuf wire format
		},
		Metadata: DLQMetadata{OriginalTopic: "orders.events.v1"},
	}

	msg, err := replay.ReplayProtoMessage(ctx(t), dlqMsg, func() proto.Message {
		return &timestamppb.Timestamp{}
	})
	require.Error(t, err)
	assert.Nil(t, msg)
	assert.Contains(t, err.Error(), "failed to unmarshal DLQ message")
}

// ctx returns a context bound to the test's lifetime.
func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return c
}
