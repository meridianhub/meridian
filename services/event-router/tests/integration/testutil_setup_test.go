//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/cockroachdb"
	kafkatc "github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/event-router/domain"
	"github.com/meridianhub/meridian/services/event-router/internal/handlers"
	sagaidempotency "github.com/meridianhub/meridian/services/event-router/internal/idempotency"
	"github.com/meridianhub/meridian/services/event-router/internal/registry"
)

// ---------- Shared containers (initialized once per test binary) ----------

var (
	crdbOnce    sync.Once
	crdbPool    *pgxpool.Pool
	crdbInitErr error

	kafkaOnce    sync.Once
	kafkaBroker  string
	kafkaInitErr error

	// kafkaCleanup is called from TestMain to terminate the Kafka container.
	kafkaCleanup func()
	crdbCleanup  func()
)

func TestMain(m *testing.M) {
	code := m.Run()

	if kafkaCleanup != nil {
		kafkaCleanup()
	}
	if crdbCleanup != nil {
		crdbCleanup()
	}
	os.Exit(code)
}

func initCockroachDB() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := cockroachdb.Run(ctx,
		"cockroachdb/cockroach:v24.3.0",
		cockroachdb.WithDatabase("test_db"),
		cockroachdb.WithUser("root"),
		cockroachdb.WithInsecure(),
	)
	if err != nil {
		crdbInitErr = fmt.Errorf("start CockroachDB: %w", err)
		return
	}

	connConfig, err := container.ConnectionConfig(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		crdbInitErr = fmt.Errorf("CockroachDB connection config: %w", err)
		return
	}

	pool, err := pgxpool.New(ctx, connConfig.ConnString())
	if err != nil {
		_ = container.Terminate(ctx)
		crdbInitErr = fmt.Errorf("CockroachDB pool: %w", err)
		return
	}

	crdbPool = pool
	crdbCleanup = func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = container.Terminate(cleanupCtx)
	}
}

func initKafka() {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	container, err := kafkatc.Run(ctx,
		"confluentinc/confluent-local:7.5.0",
		kafkatc.WithClusterID("integration-test-cluster"),
	)
	if err != nil {
		kafkaInitErr = fmt.Errorf("start Kafka: %w", err)
		return
	}

	brokers, err := container.Brokers(ctx)
	if err != nil {
		_ = container.Terminate(ctx)
		kafkaInitErr = fmt.Errorf("get Kafka brokers: %w", err)
		return
	}

	kafkaBroker = brokers[0]
	kafkaCleanup = func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = container.Terminate(cleanupCtx)
	}
}

func requireCockroachDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	crdbOnce.Do(initCockroachDB)
	if crdbInitErr != nil {
		t.Fatalf("CockroachDB init: %v", crdbInitErr)
	}
	return crdbPool
}

func requireKafka(t *testing.T) string {
	t.Helper()
	kafkaOnce.Do(initKafka)
	if kafkaInitErr != nil {
		t.Fatalf("Kafka init: %v", kafkaInitErr)
	}
	return kafkaBroker
}

// ---------- Test helpers ----------

// testLogger returns a slog logger suitable for tests.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// createTopic creates a Kafka topic with 1 partition and waits for it to be available.
func createTopic(t *testing.T, broker, topic string) {
	t.Helper()

	client, err := kgo.NewClient(kgo.SeedBrokers(broker))
	if err != nil {
		t.Fatalf("create kgo client: %v", err)
	}
	defer client.Close()

	admin := kadm.NewClient(client)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := admin.CreateTopics(ctx, 1, 1, nil, topic)
	if err != nil {
		t.Fatalf("CreateTopics: %v", err)
	}
	for _, r := range resp {
		if r.Err != nil && r.ErrMessage != "Topic already exists." {
			t.Fatalf("topic %q: %v (%s)", r.Topic, r.Err, r.ErrMessage)
		}
	}
}

// publishEvent produces a structpb.Struct as a raw Kafka record with headers.
func publishEvent(t *testing.T, broker, topic string, event map[string]any, headers map[string]string) {
	t.Helper()

	s, err := structpb.NewStruct(event)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}

	data, err := proto.Marshal(s)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}

	kgoHeaders := make([]kgo.RecordHeader, 0, len(headers))
	for k, v := range headers {
		kgoHeaders = append(kgoHeaders, kgo.RecordHeader{Key: k, Value: []byte(v)})
	}

	client, err := kgo.NewClient(kgo.SeedBrokers(broker))
	if err != nil {
		t.Fatalf("kgo.NewClient: %v", err)
	}
	defer client.Close()

	record := &kgo.Record{
		Topic:   topic,
		Value:   data,
		Headers: kgoHeaders,
	}

	results := client.ProduceSync(context.Background(), record)
	if err := results.FirstErr(); err != nil {
		t.Fatalf("produce: %v", err)
	}
}

// recordingSagaTrigger records all TriggerSaga calls for assertion.
type recordingSagaTrigger struct {
	mu    sync.Mutex
	calls []triggerRecord
}

type triggerRecord struct {
	SagaName       string
	InputData      map[string]any
	IdempotencyKey string
}

func (r *recordingSagaTrigger) TriggerSaga(_ context.Context, sagaName string, inputData map[string]any, idempotencyKey string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, triggerRecord{
		SagaName:       sagaName,
		InputData:      inputData,
		IdempotencyKey: idempotencyKey,
	})
	return "saga-instance-" + sagaName, nil
}

func (r *recordingSagaTrigger) Close() error { return nil }

func (r *recordingSagaTrigger) getCalls() []triggerRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]triggerRecord, len(r.calls))
	copy(out, r.calls)
	return out
}

func (r *recordingSagaTrigger) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// Compile-time check that recordingSagaTrigger implements domain.SagaTrigger.
var _ domain.SagaTrigger = (*recordingSagaTrigger)(nil)

// newTestRegistry creates a SagaRegistry and loads the given definitions.
func newTestRegistry(t *testing.T, defs []*controlplanev1.SagaDefinition) *registry.SagaRegistry {
	t.Helper()
	reg, err := registry.NewSagaRegistry()
	if err != nil {
		t.Fatalf("NewSagaRegistry: %v", err)
	}
	if err := reg.Reload(defs); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	return reg
}

// newTestHandler creates a SagaDispatchHandler wired with the given registry, trigger, and options.
func newTestHandler(
	reg *registry.SagaRegistry,
	trigger domain.SagaTrigger,
	opts ...handlers.Option,
) *handlers.SagaDispatchHandler {
	return handlers.NewSagaDispatchHandler(reg, trigger, opts...)
}

// newTestIdempotencyStore creates a SagaIdempotencyStore backed by the shared CockroachDB pool.
func newTestIdempotencyStore(t *testing.T, pool *pgxpool.Pool) *sagaidempotency.SagaIdempotencyStore {
	t.Helper()
	store, err := sagaidempotency.NewSagaIdempotencyStore(context.Background(), pool, &sagaidempotency.Config{
		DefaultTTL: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewSagaIdempotencyStore: %v", err)
	}
	return store
}

// newTestEvent creates a structpb.Struct as a proto.Message for direct handler testing.
func newTestEvent(t *testing.T, fields map[string]any) proto.Message {
	t.Helper()
	s, err := structpb.NewStruct(fields)
	if err != nil {
		t.Fatalf("structpb.NewStruct: %v", err)
	}
	return s
}

// Duration aliases to avoid repetitive time.Second / time.Millisecond in tests.
const (
	secondDuration      = time.Second
	millisecondDuration = time.Millisecond
)

// consumeAndDispatch starts a background goroutine that consumes Kafka messages
// from the given topic and dispatches them through the SagaDispatchHandler.
// The consumer is stopped when the test finishes.
//
// Errors from proto unmarshalling and handler dispatch are logged via the
// provided logger rather than silently discarded, so test failures are
// diagnosable from the test output.
func consumeAndDispatch(t *testing.T, broker, topic string, h *handlers.SagaDispatchHandler) {
	t.Helper()

	logger := testLogger()

	client, err := kgo.NewClient(
		kgo.SeedBrokers(broker),
		kgo.ConsumeTopics(topic),
		kgo.ConsumerGroup("integration-test-"+topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		client.Close()
	})

	go func() {
		for {
			fetches := client.PollFetches(ctx)
			if ctx.Err() != nil {
				return
			}

			fetches.EachRecord(func(record *kgo.Record) {
				// Reconstruct proto message from record value
				s := &structpb.Struct{}
				if unmarshalErr := proto.Unmarshal(record.Value, s); unmarshalErr != nil {
					logger.Error("failed to unmarshal Kafka record",
						"topic", record.Topic,
						"offset", record.Offset,
						"error", unmarshalErr,
					)
					return
				}

				// Extract metadata from Kafka headers
				metadata := make(map[string]string)
				for _, header := range record.Headers {
					metadata[header.Key] = string(header.Value)
				}

				if handleErr := h.Handle(ctx, record.Topic, s, metadata); handleErr != nil {
					logger.Error("handler dispatch failed",
						"topic", record.Topic,
						"offset", record.Offset,
						"error", handleErr,
					)
				}
			})

			client.AllowRebalance()
		}
	}()
}
