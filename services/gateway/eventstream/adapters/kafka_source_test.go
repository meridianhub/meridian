package adapters

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/gateway/eventstream"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// ─── unit tests ────────────────────────────────────────────────────────────────

func TestNewKafkaEventSource_Validation(t *testing.T) {
	tests := []struct {
		name    string
		brokers string
		topics  []string
		wantErr bool
	}{
		{
			name:    "empty bootstrap servers",
			brokers: "",
			topics:  []string{"t.v1"},
			wantErr: true,
		},
		{
			name:    "empty topics",
			brokers: "localhost:9092",
			topics:  []string{},
			wantErr: true,
		},
		{
			name:    "nil topics",
			brokers: "localhost:9092",
			topics:  nil,
			wantErr: true,
		},
		{
			name:    "valid args creates client without error",
			brokers: "localhost:9092",
			topics:  []string{"events.v1"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, err := NewKafkaEventSource(tt.brokers, tt.topics, nil)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewKafkaEventSource() error = %v, wantErr %v", err, tt.wantErr)
			}
			if src != nil && src.client != nil {
				// Close immediately to release resources.
				src.client.Close()
			}
		})
	}
}

func TestExtractHeaderValue(t *testing.T) {
	record := &kgo.Record{
		Headers: []kgo.RecordHeader{
			{Key: "a", Value: []byte("v-a")},
			{Key: "b", Value: []byte("v-b")},
		},
	}

	if got := extractHeaderValue(record, "a"); got != "v-a" {
		t.Errorf("extractHeaderValue(a) = %q, want %q", got, "v-a")
	}
	if got := extractHeaderValue(record, "b"); got != "v-b" {
		t.Errorf("extractHeaderValue(b) = %q, want %q", got, "v-b")
	}
	if got := extractHeaderValue(record, "missing"); got != "" {
		t.Errorf("extractHeaderValue(missing) = %q, want %q", got, "")
	}
}

func TestExtractHeader_Missing(t *testing.T) {
	record := &kgo.Record{}
	_, err := extractHeader(record, "not-there")
	if err == nil {
		t.Fatal("expected error for missing header")
	}
}

func TestEncodePayload(t *testing.T) {
	t.Run("nil input returns nil", func(t *testing.T) {
		if got := encodePayload(nil); got != nil {
			t.Fatalf("encodePayload(nil) = %v, want nil", got)
		}
	})

	t.Run("empty input returns nil", func(t *testing.T) {
		if got := encodePayload([]byte{}); got != nil {
			t.Fatalf("encodePayload([]) = %v, want nil", got)
		}
	})

	t.Run("valid JSON passes through verbatim", func(t *testing.T) {
		input := []byte(`{"foo":"bar"}`)
		got := encodePayload(input)
		if string(got) != string(input) {
			t.Fatalf("encodePayload(%q) = %q, want %q", input, got, input)
		}
	})

	t.Run("binary bytes are base64-encoded JSON string", func(t *testing.T) {
		// Simulate binary protobuf: non-JSON bytes.
		input := []byte{0x0a, 0x05, 0x68, 0x65, 0x6c, 0x6c, 0x6f}
		got := encodePayload(input)

		// Must be valid JSON.
		if !json.Valid(got) {
			t.Fatalf("encodePayload returned invalid JSON: %s", got)
		}

		// Unwrap the JSON string to recover the base64 value.
		var encoded string
		if err := json.Unmarshal(got, &encoded); err != nil {
			t.Fatalf("json.Unmarshal: %v", err)
		}

		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatalf("base64.Decode: %v", err)
		}
		if string(decoded) != string(input) {
			t.Fatalf("round-trip mismatch: got %v, want %v", decoded, input)
		}
	})
}

func TestRecordToDomainEvent(t *testing.T) {
	src := &KafkaEventSource{logger: newDiscardLogger()}

	t.Run("happy path", func(t *testing.T) {
		payload := []byte(`{"amount":"100.00"}`)
		record := &kgo.Record{
			Topic:     "payment-order.created.v1",
			Timestamp: time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC),
			Value:     payload,
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
				{Key: "event_id", Value: []byte("evt-123")},
				{Key: "event_type", Value: []byte("payment_order.created.v1")},
				{Key: "aggregate_type", Value: []byte("PaymentOrder")},
				{Key: "aggregate_id", Value: []byte("po-456")},
				{Key: "correlation_id", Value: []byte("corr-789")},
				{Key: "causation_id", Value: []byte("cause-000")},
			},
		}

		event, err := src.recordToDomainEvent(record)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		assertString(t, "EventID", event.EventID, "evt-123")
		assertString(t, "EventType", event.EventType, "payment_order.created.v1")
		assertString(t, "Topic", event.Topic, "payment-order.created.v1")
		assertString(t, "Channel", event.Channel, "payment-order.created")
		assertString(t, "AggregateType", event.AggregateType, "PaymentOrder")
		assertString(t, "AggregateID", event.AggregateID, "po-456")
		assertString(t, "TenantID", event.TenantID, "acme_bank")
		assertString(t, "CorrelationID", event.CorrelationID, "corr-789")
		assertString(t, "CausationID", event.CausationID, "cause-000")

		if !event.Timestamp.Equal(record.Timestamp.UTC()) {
			t.Errorf("Timestamp = %v, want %v", event.Timestamp, record.Timestamp.UTC())
		}

		if string(event.Payload) != string(payload) {
			t.Errorf("Payload = %q, want %q", event.Payload, payload)
		}
	})

	t.Run("missing tenant header returns error", func(t *testing.T) {
		record := &kgo.Record{
			Topic: "events.v1",
			Headers: []kgo.RecordHeader{
				{Key: "event_type", Value: []byte("some.event")},
			},
		}
		_, err := src.recordToDomainEvent(record)
		if err == nil {
			t.Fatal("expected error for missing tenant header")
		}
	})

	t.Run("missing event_type header returns error", func(t *testing.T) {
		record := &kgo.Record{
			Topic: "events.v1",
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
			},
		}
		_, err := src.recordToDomainEvent(record)
		if err == nil {
			t.Fatal("expected error for missing event_type header")
		}
	})

	t.Run("zero timestamp defaults to now", func(t *testing.T) {
		before := time.Now().UTC().Add(-time.Second)
		record := &kgo.Record{
			Topic:     "events.v1",
			Timestamp: time.Time{}, // zero
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
				{Key: "event_type", Value: []byte("some.event")},
			},
		}

		event, err := src.recordToDomainEvent(record)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event.Timestamp.Before(before) {
			t.Errorf("Timestamp %v is before test start %v", event.Timestamp, before)
		}
	})

	t.Run("no event_id header generates uuid", func(t *testing.T) {
		record := &kgo.Record{
			Topic: "events.v1",
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
				{Key: "event_type", Value: []byte("some.event")},
				// no event_id header
			},
		}

		event, err := src.recordToDomainEvent(record)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event.EventID == "" {
			t.Error("EventID should not be empty when header is absent")
		}
	})

	t.Run("binary protobuf payload is base64 encoded", func(t *testing.T) {
		binaryPayload := []byte{0x0a, 0x05, 0x68, 0x65, 0x6c, 0x6c, 0x6f}
		record := &kgo.Record{
			Topic: "events.v1",
			Value: binaryPayload,
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
				{Key: "event_type", Value: []byte("some.event")},
			},
		}

		event, err := src.recordToDomainEvent(record)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !json.Valid(event.Payload) {
			t.Errorf("Payload is not valid JSON: %s", event.Payload)
		}
	})
}

func TestExtractChainDepth(t *testing.T) {
	tests := []struct {
		name    string
		headers []kgo.RecordHeader
		want    int
	}{
		{
			name:    "no chain depth header defaults to 0",
			headers: nil,
			want:    0,
		},
		{
			name: "chain depth 0",
			headers: []kgo.RecordHeader{
				{Key: headerChainDepth, Value: []byte("0")},
			},
			want: 0,
		},
		{
			name: "chain depth 3",
			headers: []kgo.RecordHeader{
				{Key: headerChainDepth, Value: []byte("3")},
			},
			want: 3,
		},
		{
			name: "chain depth 8",
			headers: []kgo.RecordHeader{
				{Key: headerChainDepth, Value: []byte("8")},
			},
			want: 8,
		},
		{
			name: "invalid non-numeric value defaults to 0",
			headers: []kgo.RecordHeader{
				{Key: headerChainDepth, Value: []byte("not-a-number")},
			},
			want: 0,
		},
		{
			name: "negative value defaults to 0",
			headers: []kgo.RecordHeader{
				{Key: headerChainDepth, Value: []byte("-1")},
			},
			want: 0,
		},
		{
			name: "empty value defaults to 0",
			headers: []kgo.RecordHeader{
				{Key: headerChainDepth, Value: []byte("")},
			},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := &kgo.Record{Headers: tt.headers}
			got := extractChainDepth(record)
			if got != tt.want {
				t.Errorf("extractChainDepth() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestIncrementChainDepth(t *testing.T) {
	tests := []struct {
		depth int
		want  int
	}{
		{0, 1},
		{1, 2},
		{7, 8},
		{99, 100},
	}
	for _, tt := range tests {
		if got := incrementChainDepth(tt.depth); got != tt.want {
			t.Errorf("incrementChainDepth(%d) = %d, want %d", tt.depth, got, tt.want)
		}
	}
}

func TestRecordToDomainEvent_ChainDepth(t *testing.T) {
	src := &KafkaEventSource{logger: newDiscardLogger()}

	t.Run("chain depth header extracted", func(t *testing.T) {
		record := &kgo.Record{
			Topic: "events.v1",
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
				{Key: "event_type", Value: []byte("some.event")},
				{Key: headerChainDepth, Value: []byte("5")},
			},
		}
		event, err := src.recordToDomainEvent(record)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event.ChainDepth != 5 {
			t.Errorf("ChainDepth = %d, want 5", event.ChainDepth)
		}
	})

	t.Run("missing chain depth header defaults to 0", func(t *testing.T) {
		record := &kgo.Record{
			Topic: "events.v1",
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
				{Key: "event_type", Value: []byte("some.event")},
			},
		}
		event, err := src.recordToDomainEvent(record)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if event.ChainDepth != 0 {
			t.Errorf("ChainDepth = %d, want 0", event.ChainDepth)
		}
	})
}

// ─── integration tests (require Kafka testcontainer) ───────────────────────────

func TestKafkaEventSource_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Kafka integration test in short mode")
	}

	ctx := context.Background()

	kafkaContainer, err := kafka.Run(ctx,
		"confluentinc/confluent-local:7.5.0",
		kafka.WithClusterID("test-cluster"),
	)
	if err != nil {
		t.Fatalf("failed to start Kafka container: %v", err)
	}
	t.Cleanup(func() {
		if err := kafkaContainer.Terminate(ctx); err != nil {
			t.Logf("failed to terminate Kafka container: %v", err)
		}
	})

	brokers, err := kafkaContainer.Brokers(ctx)
	if err != nil {
		t.Fatalf("failed to get brokers: %v", err)
	}
	if len(brokers) == 0 {
		t.Fatal("kafka container returned no brokers")
	}

	const topic = "test-events.v1"
	brokerAddr := brokers[0]

	// Create topics before tests run so producers don't get UNKNOWN_TOPIC_OR_PARTITION.
	mustCreateTopics(t, ctx, brokerAddr, topic, "test-cg-events.v1")

	t.Run("publishes event to handler and derives channel", func(t *testing.T) {
		src, err := NewKafkaEventSource(brokerAddr, []string{topic}, newDiscardLogger())
		if err != nil {
			t.Fatalf("NewKafkaEventSource: %v", err)
		}

		// Produce a test record before starting the consumer (source starts at latest).
		producer, err := kgo.NewClient(kgo.SeedBrokers(brokerAddr))
		if err != nil {
			t.Fatalf("failed to create producer: %v", err)
		}
		defer producer.Close()

		// Give the consumer group a chance to join before we produce.
		consumeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		received := make(chan eventstream.DomainEvent, 1)

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := src.Start(consumeCtx, func(_ context.Context, ev eventstream.DomainEvent) error {
				select {
				case received <- ev:
				default:
				}
				return nil
			})
			if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
				t.Errorf("Start returned unexpected error: %v", err)
			}
		}()

		// Poll until the consumer group has at least one active member.
		if err := await.New().
			AtMost(10 * time.Second).
			PollInterval(200 * time.Millisecond).
			UntilNoError(func() error {
				return consumerGroupActive(ctx, brokerAddr, ConsumerGroupID)
			}); err != nil {
			t.Fatalf("consumer group did not become active: %v", err)
		}

		// Produce a record now that the consumer is at the end.
		record := &kgo.Record{
			Topic: topic,
			Value: []byte(`{"amount":"99.00"}`),
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("acme_bank")},
				{Key: "event_id", Value: []byte("evt-integration-1")},
				{Key: "event_type", Value: []byte("test.created.v1")},
				{Key: "aggregate_type", Value: []byte("Test")},
				{Key: "aggregate_id", Value: []byte("test-agg-1")},
			},
		}
		results := producer.ProduceSync(ctx, record)
		if err := results.FirstErr(); err != nil {
			t.Fatalf("ProduceSync: %v", err)
		}

		// Wait for the event to arrive.
		select {
		case ev := <-received:
			assertString(t, "EventID", ev.EventID, "evt-integration-1")
			assertString(t, "TenantID", ev.TenantID, "acme_bank")
			assertString(t, "Topic", ev.Topic, topic)
			assertString(t, "Channel", ev.Channel, "test-events")
			if !json.Valid(ev.Payload) {
				t.Errorf("Payload is not valid JSON: %s", ev.Payload)
			}
		case <-consumeCtx.Done():
			t.Fatal("timed out waiting for event")
		}

		cancel()
		wg.Wait()
	})

	t.Run("graceful shutdown on context cancel", func(t *testing.T) {
		src, err := NewKafkaEventSource(brokerAddr, []string{topic}, newDiscardLogger())
		if err != nil {
			t.Fatalf("NewKafkaEventSource: %v", err)
		}

		ctx2, cancel2 := context.WithCancel(ctx)

		done := make(chan error, 1)
		go func() {
			done <- src.Start(ctx2, func(_ context.Context, _ eventstream.DomainEvent) error {
				return nil
			})
		}()

		// Wait for the consumer to start polling before cancelling.
		if err := await.New().
			AtMost(5 * time.Second).
			PollInterval(100 * time.Millisecond).
			UntilNoError(func() error {
				return consumerGroupActive(ctx, brokerAddr, ConsumerGroupID)
			}); err != nil {
			t.Logf("consumer group may not be fully active: %v (continuing)", err)
		}
		cancel2()

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Start returned non-nil error after cancel: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Start did not return after context cancel")
		}
	})

	t.Run("consumer group management — second consumer sees same events", func(t *testing.T) {
		const topic2 = "test-cg-events.v1"

		// Produce one record to the topic before consumers join.
		producer, err := kgo.NewClient(kgo.SeedBrokers(brokerAddr))
		if err != nil {
			t.Fatalf("producer: %v", err)
		}
		defer producer.Close()

		// Both sources share the same consumer group; each will get a partition subset.
		src1, err := NewKafkaEventSource(brokerAddr, []string{topic2}, newDiscardLogger())
		if err != nil {
			t.Fatalf("src1: %v", err)
		}
		src2, err := NewKafkaEventSource(brokerAddr, []string{topic2}, newDiscardLogger())
		if err != nil {
			t.Fatalf("src2: %v", err)
		}

		ctx3, cancel3 := context.WithTimeout(ctx, 20*time.Second)
		defer cancel3()

		received := make(chan struct{}, 2)
		handler := func(_ context.Context, _ eventstream.DomainEvent) error {
			received <- struct{}{}
			return nil
		}

		var wg2 sync.WaitGroup
		wg2.Add(2)
		go func() { defer wg2.Done(); _ = src1.Start(ctx3, handler) }()
		go func() { defer wg2.Done(); _ = src2.Start(ctx3, handler) }()

		// Poll until at least one consumer has joined the group.
		if err := await.New().
			AtMost(15 * time.Second).
			PollInterval(200 * time.Millisecond).
			UntilNoError(func() error {
				return consumerGroupActive(ctx3, brokerAddr, ConsumerGroupID)
			}); err != nil {
			t.Fatalf("consumer group did not become active: %v", err)
		}

		// Produce a record — only one consumer will receive it (partition assignment).
		r := &kgo.Record{
			Topic: topic2,
			Value: []byte(`{}`),
			Headers: []kgo.RecordHeader{
				{Key: tenant.TenantIDKey, Value: []byte("tenant_a")},
				{Key: "event_type", Value: []byte("cg.test.v1")},
			},
		}
		if err := producer.ProduceSync(ctx, r).FirstErr(); err != nil {
			t.Fatalf("ProduceSync: %v", err)
		}

		select {
		case <-received:
			// At least one consumer received the event — group is working.
		case <-ctx3.Done():
			t.Fatal("timed out: no consumer received the event")
		}

		cancel3()
		wg2.Wait()
	})
}

// ─── helpers ───────────────────────────────────────────────────────────────────

func assertString(t *testing.T, field, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %q, want %q", field, got, want)
	}
}

func newDiscardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// consumerGroupActive returns nil when the ops-console-events consumer group has at
// least one active member, or a non-nil error if it is empty or does not exist.
// Used with await.UntilNoError to replace time.Sleep in integration tests.
func consumerGroupActive(ctx context.Context, broker, groupID string) error {
	client, err := kgo.NewClient(kgo.SeedBrokers(broker))
	if err != nil {
		return fmt.Errorf("create client: %w", err)
	}
	defer client.Close()

	admin := kadm.NewClient(client)
	described, err := admin.DescribeGroups(ctx, groupID)
	if err != nil {
		return fmt.Errorf("describe groups: %w", err)
	}
	grp, ok := described[groupID]
	if !ok || grp.Err != nil || len(grp.Members) == 0 {
		return fmt.Errorf("group %q has no active members", groupID)
	}
	return nil
}

// mustCreateTopics creates the given topics in the Kafka broker using kadm,
// failing the test if any creation fails (ignoring "topic already exists" errors).
func mustCreateTopics(t *testing.T, ctx context.Context, broker string, topics ...string) {
	t.Helper()

	client, err := kgo.NewClient(kgo.SeedBrokers(broker))
	if err != nil {
		t.Fatalf("mustCreateTopics: failed to create kgo client: %v", err)
	}
	defer client.Close()

	admin := kadm.NewClient(client)
	resp, err := admin.CreateTopics(ctx, 1, 1, nil, topics...)
	if err != nil {
		t.Fatalf("mustCreateTopics: CreateTopics failed: %v", err)
	}
	for _, tr := range resp {
		if tr.Err != nil {
			// "topic already exists" is fine.
			if tr.ErrMessage == "Topic already exists." {
				continue
			}
			t.Fatalf("mustCreateTopics: error for topic %q: %v (%s)", tr.Topic, tr.Err, tr.ErrMessage)
		}
	}
}
