// Package kafka provides DLQ administrative and monitoring utilities.
package kafka

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/meridianhub/meridian/shared/platform/defaults"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
)

// ErrNilInspector is returned when DLQ inspector is nil.
var ErrNilInspector = errors.New("DLQ inspector cannot be nil")

// DLQMessage represents a message in the dead letter queue with parsed metadata.
type DLQMessage struct {
	// Record is the original Kafka record from the DLQ topic
	Record *kgo.Record
	// Metadata contains parsed DLQ headers
	Metadata DLQMetadata
}

// DLQInspectorConfig configures the DLQ inspector behavior.
type DLQInspectorConfig struct {
	// BootstrapServers is the comma-separated list of Kafka broker addresses (required).
	BootstrapServers string
	// ClientID identifies the inspector for logging and metrics (optional).
	ClientID string
	// DLQTopics is the list of DLQ topic names to inspect (required).
	DLQTopics []string
}

// DLQInspector provides utilities for examining and analyzing dead letter queue messages.
type DLQInspector struct {
	client *kgo.Client
	admin  *kadm.Client
	config DLQInspectorConfig
}

// NewDLQInspector creates a new DLQ inspector for examining failed messages.
//
// Parameters:
// - config: Inspector configuration with broker addresses and DLQ topics
//
// Returns an error if configuration is invalid or consumer creation fails.
func NewDLQInspector(config DLQInspectorConfig) (*DLQInspector, error) {
	if config.BootstrapServers == "" {
		return nil, ErrEmptyBootstrapServers
	}
	if len(config.DLQTopics) == 0 {
		return nil, ErrEmptyTopics
	}

	// Build franz-go options for inspection (read-only, no consumer group)
	opts := []kgo.Opt{
		kgo.SeedBrokers(splitBrokers(config.BootstrapServers)...),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	}

	if config.ClientID != "" {
		opts = append(opts, kgo.ClientID(config.ClientID))
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create DLQ inspector client: %w", err)
	}

	// Create admin client for metadata operations
	admin := kadm.NewClient(client)

	return &DLQInspector{
		client: client,
		admin:  admin,
		config: config,
	}, nil
}

// parseDLQMetadata extracts DLQ metadata from Kafka record headers.
func parseDLQMetadata(record *kgo.Record) DLQMetadata {
	metadata := DLQMetadata{}

	for _, header := range record.Headers {
		value := string(header.Value)

		switch header.Key {
		case "dlq.original_topic":
			metadata.OriginalTopic = value
		case "dlq.original_partition":
			if partition, err := strconv.ParseInt(value, 10, 32); err == nil {
				metadata.OriginalPartition = int32(partition)
			}
		case "dlq.original_offset":
			if offset, err := strconv.ParseInt(value, 10, 64); err == nil {
				metadata.OriginalOffset = offset
			}
		case "dlq.error_message":
			metadata.ErrorMessage = value
		case "dlq.error_stack_trace":
			metadata.ErrorStackTrace = value
		case "dlq.retry_count":
			if retries, err := strconv.ParseInt(value, 10, 32); err == nil {
				metadata.RetryCount = int32(retries)
			}
		case "dlq.first_failure_time":
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				metadata.FirstFailureTime = t
			}
		case "dlq.last_failure_time":
			if t, err := time.Parse(time.RFC3339, value); err == nil {
				metadata.LastFailureTime = t
			}
		case "dlq.consumer_group_id":
			metadata.ConsumerGroupID = value
		case "dlq.correlation_id":
			metadata.CorrelationID = value
		case "dlq.causation_id":
			metadata.CausationID = value
		}
	}

	return metadata
}

// FilterFunc is a predicate function for filtering DLQ messages.
// Return true to include the message in results, false to exclude it.
type FilterFunc func(msg DLQMessage) bool

// FilterByErrorType creates a filter that matches messages with errors containing the specified text.
func FilterByErrorType(errorText string) FilterFunc {
	return func(msg DLQMessage) bool {
		return strings.Contains(strings.ToLower(msg.Metadata.ErrorMessage), strings.ToLower(errorText))
	}
}

// FilterByOriginalTopic creates a filter that matches messages from a specific original topic.
func FilterByOriginalTopic(topic string) FilterFunc {
	return func(msg DLQMessage) bool {
		return msg.Metadata.OriginalTopic == topic
	}
}

// FilterByTimeRange creates a filter that matches messages that failed within a time range.
func FilterByTimeRange(start, end time.Time) FilterFunc {
	return func(msg DLQMessage) bool {
		return !msg.Metadata.LastFailureTime.Before(start) && !msg.Metadata.LastFailureTime.After(end)
	}
}

// FilterByConsumerGroup creates a filter that matches messages from a specific consumer group.
func FilterByConsumerGroup(groupID string) FilterFunc {
	return func(msg DLQMessage) bool {
		return msg.Metadata.ConsumerGroupID == groupID
	}
}

// CombineFilters combines multiple filters with AND logic.
// All filters must return true for a message to be included.
func CombineFilters(filters ...FilterFunc) FilterFunc {
	return func(msg DLQMessage) bool {
		for _, filter := range filters {
			if !filter(msg) {
				return false
			}
		}
		return true
	}
}

// InspectOptions configures the behavior of the Inspect method.
type InspectOptions struct {
	// Filter is an optional predicate to filter messages. If nil, all messages are returned.
	Filter FilterFunc
	// MaxMessages limits the number of messages to return. 0 means no limit.
	MaxMessages int
	// Timeout is the maximum duration to wait for messages.
	Timeout time.Duration
}

// Inspect reads messages from the DLQ topics and returns them according to the filter criteria.
// This method does not commit offsets - it's read-only for inspection purposes.
//
// Parameters:
// - ctx: Context for cancellation
// - options: Configuration for filtering and limiting results
//
// Returns a slice of DLQ messages that match the filter criteria.
func (i *DLQInspector) Inspect(ctx context.Context, options InspectOptions) ([]DLQMessage, error) {
	if err := i.assignDLQPartitions(ctx); err != nil {
		return nil, err
	}

	timeout := options.Timeout
	if timeout == 0 {
		timeout = defaults.DefaultRPCTimeout
	}

	inspectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return i.pollDLQMessages(inspectCtx, options)
}

// assignDLQPartitions fetches topic metadata and assigns all partitions for reading.
func (i *DLQInspector) assignDLQPartitions(ctx context.Context) error {
	topicDetails, err := i.admin.ListTopics(ctx, i.config.DLQTopics...)
	if err != nil {
		return fmt.Errorf("failed to list topics: %w", err)
	}

	partitions := make(map[string]map[int32]kgo.Offset)
	for _, topic := range i.config.DLQTopics {
		details, ok := topicDetails[topic]
		if !ok {
			continue
		}
		partitions[topic] = make(map[int32]kgo.Offset)
		for _, partition := range details.Partitions {
			partitions[topic][partition.Partition] = kgo.NewOffset().AtStart()
		}
	}

	i.client.AddConsumePartitions(partitions)
	return nil
}

// pollDLQMessages polls for DLQ messages until context expires, message limit is reached,
// or consecutive empty polls indicate no more messages.
func (i *DLQInspector) pollDLQMessages(ctx context.Context, options InspectOptions) ([]DLQMessage, error) {
	results := make([]DLQMessage, 0)
	consecutiveEmptyPolls := 0
	maxConsecutiveEmptyPolls := 5

	for {
		select {
		case <-ctx.Done():
			return results, fmt.Errorf("inspect context canceled: %w", ctx.Err())
		default:
		}

		if options.MaxMessages > 0 && len(results) >= options.MaxMessages {
			return results, nil
		}

		pollCtx, pollCancel := context.WithTimeout(ctx, 100*time.Millisecond)
		fetches := i.client.PollFetches(pollCtx)
		pollCancel()

		if err := checkFetchErrors(fetches); err != nil {
			return results, err
		}

		if fetches.Empty() {
			consecutiveEmptyPolls++
			if consecutiveEmptyPolls >= maxConsecutiveEmptyPolls {
				return results, nil
			}
			continue
		}

		consecutiveEmptyPolls = 0

		fetches.EachRecord(func(record *kgo.Record) {
			if options.MaxMessages > 0 && len(results) >= options.MaxMessages {
				return
			}

			metadata := parseDLQMetadata(record)
			dlqMsg := DLQMessage{
				Record:   record,
				Metadata: metadata,
			}

			if options.Filter != nil && !options.Filter(dlqMsg) {
				return
			}

			results = append(results, dlqMsg)
		})
	}
}

// checkFetchErrors returns the first non-timeout fetch error, or nil.
func checkFetchErrors(fetches kgo.Fetches) error {
	if errs := fetches.Errors(); len(errs) > 0 {
		for _, err := range errs {
			if errors.Is(err.Err, context.DeadlineExceeded) || errors.Is(err.Err, context.Canceled) {
				continue
			}
			return fmt.Errorf("error reading DLQ message: %w", err.Err)
		}
	}
	return nil
}

// DLQStatistics contains aggregate statistics about DLQ messages.
type DLQStatistics struct {
	// TotalMessages is the total number of messages in the DLQ
	TotalMessages int
	// MessagesByTopic maps original topic names to message counts
	MessagesByTopic map[string]int
	// MessagesByErrorType maps error types to message counts
	MessagesByErrorType map[string]int
	// MessagesByConsumerGroup maps consumer group IDs to message counts
	MessagesByConsumerGroup map[string]int
	// OldestFailure is the timestamp of the oldest message in DLQ
	OldestFailure time.Time
	// NewestFailure is the timestamp of the newest message in DLQ
	NewestFailure time.Time
}

// GetStatistics analyzes all messages in the DLQ and returns aggregate statistics.
func (i *DLQInspector) GetStatistics(ctx context.Context, timeout time.Duration) (DLQStatistics, error) {
	messages, err := i.Inspect(ctx, InspectOptions{
		Filter:      nil, // No filter - get all messages
		MaxMessages: 0,   // No limit
		Timeout:     timeout,
	})
	if err != nil {
		return DLQStatistics{}, err
	}

	stats := DLQStatistics{
		TotalMessages:           len(messages),
		MessagesByTopic:         make(map[string]int),
		MessagesByErrorType:     make(map[string]int),
		MessagesByConsumerGroup: make(map[string]int),
	}

	for _, msg := range messages {
		// Count by original topic
		stats.MessagesByTopic[msg.Metadata.OriginalTopic]++

		// Count by error type (extract first line of error message)
		errorType := strings.Split(msg.Metadata.ErrorMessage, "\n")[0]
		if len(errorType) > 100 {
			errorType = errorType[:100] + "..."
		}
		stats.MessagesByErrorType[errorType]++

		// Count by consumer group
		stats.MessagesByConsumerGroup[msg.Metadata.ConsumerGroupID]++

		// Track oldest/newest failures
		if stats.OldestFailure.IsZero() || msg.Metadata.FirstFailureTime.Before(stats.OldestFailure) {
			stats.OldestFailure = msg.Metadata.FirstFailureTime
		}
		if msg.Metadata.LastFailureTime.After(stats.NewestFailure) {
			stats.NewestFailure = msg.Metadata.LastFailureTime
		}
	}

	return stats, nil
}

// Close closes the inspector and releases resources.
func (i *DLQInspector) Close() error {
	i.client.Close()
	return nil
}

// DLQReplayConfig configures DLQ message replay behavior.
type DLQReplayConfig struct {
	// BootstrapServers is the comma-separated list of Kafka broker addresses (required).
	BootstrapServers string
	// ClientID identifies the replay producer for logging and metrics (optional).
	ClientID string
}

// DLQReplay provides utilities for reprocessing messages from the dead letter queue.
type DLQReplay struct {
	producer *ProtoProducer
	config   DLQReplayConfig
}

// NewDLQReplay creates a new DLQ replay utility for reprocessing failed messages.
//
// Parameters:
// - config: Replay configuration with broker addresses
//
// Returns an error if configuration is invalid or producer creation fails.
func NewDLQReplay(config DLQReplayConfig) (*DLQReplay, error) {
	producer, err := NewProtoProducer(ProducerConfig{
		BootstrapServers: config.BootstrapServers,
		ClientID:         config.ClientID,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create DLQ replay producer: %w", err)
	}

	return &DLQReplay{
		producer: producer,
		config:   config,
	}, nil
}

// ReplayMessage sends a DLQ message back to its original topic for reprocessing.
// The message is sent exactly as it was originally produced (same key and value).
// DLQ headers are removed so the message appears as a new message to consumers.
//
// Parameters:
// - ctx: Context for cancellation and timeout control
// - dlqMsg: The DLQ message to replay
//
// Returns an error if publishing fails.
func (r *DLQReplay) ReplayMessage(ctx context.Context, dlqMsg DLQMessage) error {
	// Create new record without DLQ headers
	// Preserve original timestamp to maintain event-time semantics
	replayRecord := &kgo.Record{
		Topic:     dlqMsg.Metadata.OriginalTopic,
		Key:       dlqMsg.Record.Key,
		Value:     dlqMsg.Record.Value,
		Timestamp: dlqMsg.Record.Timestamp,
	}

	// Preserve non-DLQ headers
	for _, header := range dlqMsg.Record.Headers {
		if !strings.HasPrefix(header.Key, "dlq.") {
			replayRecord.Headers = append(replayRecord.Headers, header)
		}
	}

	// Publish synchronously and wait for confirmation
	return r.producer.ProduceRecord(ctx, replayRecord)
}

// ReplayMessages replays multiple DLQ messages back to their original topics.
// Messages are sent in order. If any message fails, the method returns the error
// and stops processing remaining messages.
//
// Parameters:
// - ctx: Context for cancellation and timeout control
// - messages: The DLQ messages to replay
//
// Returns the number of messages successfully replayed and any error encountered.
func (r *DLQReplay) ReplayMessages(ctx context.Context, messages []DLQMessage) (int, error) {
	successCount := 0
	for i, msg := range messages {
		if err := r.ReplayMessage(ctx, msg); err != nil {
			return successCount, fmt.Errorf("failed to replay message %d/%d: %w", i+1, len(messages), err)
		}
		successCount++
	}
	return successCount, nil
}

// Close closes the replay producer and releases resources.
func (r *DLQReplay) Close() {
	r.producer.Close()
}

// ReplayProtoMessage is a convenience method for replaying a protobuf message.
// This deserializes the DLQ message value using the provided factory and
// re-serializes it before sending to the original topic.
//
// Note: This is primarily useful for transformation scenarios. For simple replay,
// use ReplayMessage to preserve the exact original bytes.
//
// Parameters:
// - ctx: Context for cancellation and timeout control
// - dlqMsg: The DLQ message to replay
// - msgFactory: Function to create a new proto message instance
//
// Returns the deserialized proto message and any error encountered.
func (r *DLQReplay) ReplayProtoMessage(
	ctx context.Context,
	dlqMsg DLQMessage,
	msgFactory func() proto.Message,
) (proto.Message, error) {
	// Deserialize the message
	protoMsg := msgFactory()
	if err := proto.Unmarshal(dlqMsg.Record.Value, protoMsg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal DLQ message: %w", err)
	}

	// Use standard replay (preserves original bytes)
	if err := r.ReplayMessage(ctx, dlqMsg); err != nil {
		return nil, err
	}

	return protoMsg, nil
}
