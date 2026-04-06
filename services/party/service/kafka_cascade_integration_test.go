package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	kafkatc "github.com/testcontainers/testcontainers-go/modules/kafka"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
	pb "github.com/meridianhub/meridian/api/proto/meridian/party/v1"
	"github.com/meridianhub/meridian/services/party/adapters/persistence"
	"github.com/meridianhub/meridian/shared/platform/audit"
	"github.com/meridianhub/meridian/shared/platform/await"
	"github.com/meridianhub/meridian/shared/platform/events"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
	"github.com/meridianhub/meridian/shared/platform/tenant"
	"github.com/meridianhub/meridian/shared/platform/testdb"
)

// testKafkaPublisher adapts a raw kgo.Client to the events.KafkaPublisher interface.
// Used in tests to avoid idempotent write issues with single-broker testcontainers.
type testKafkaPublisher struct {
	client *kgo.Client
}

func (p *testKafkaPublisher) ProduceRecord(ctx context.Context, record *kgo.Record) error {
	results := p.client.ProduceSync(ctx, record)
	return results.FirstErr()
}

func (p *testKafkaPublisher) Flush(ctx context.Context) error {
	return p.client.Flush(ctx)
}

func (p *testKafkaPublisher) FlushWithTimeout(timeoutMs int) int {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()
	if err := p.client.Flush(ctx); err != nil {
		return 1
	}
	return 0
}

func (p *testKafkaPublisher) Close() {
	p.client.Close()
}

// kafkaCascadeTestEnv holds the shared test infrastructure for Kafka cascade tests.
type kafkaCascadeTestEnv struct {
	svc        *Service
	db         *gorm.DB
	ctx        context.Context
	broker     string
	worker     *events.Worker
	cleanup    func()
	workerStop func()
}

// setupKafkaCascadeTest creates CockroachDB + Kafka testcontainers, wires the party service
// with an outbox publisher and outbox worker, and returns an environment ready for testing
// the full outbox-to-Kafka event pipeline.
func setupKafkaCascadeTest(t *testing.T) *kafkaCascadeTestEnv {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping Kafka cascade integration test in short mode")
	}

	bgCtx := context.Background()

	// Start Kafka testcontainer
	kafkaContainer, err := kafkatc.Run(bgCtx,
		"confluentinc/confluent-local:7.5.0",
		kafkatc.WithClusterID("cascade-test-cluster"),
	)
	require.NoError(t, err, "failed to start Kafka container")

	brokers, err := kafkaContainer.Brokers(bgCtx)
	require.NoError(t, err)
	require.NotEmpty(t, brokers)
	broker := brokers[0]

	// Create the party.controlled.v1 topic
	createKafkaTopic(t, broker, topics.PartyControlledV1)

	// Setup CockroachDB with party + event_outbox tables
	db, dbCleanup := testdb.SetupPostgres(t, []interface{}{
		&persistence.PartyEntity{},
		&audit.AuditOutbox{},
		&events.EventOutbox{},
	})

	tid := tenant.TenantID(testTenantID)
	schemaName := tid.SchemaName()

	// Create tenant schema and tables
	require.NoError(t, db.Exec(fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s", pq.QuoteIdentifier(schemaName))).Error)
	require.NoError(t, db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.party (
		id UUID PRIMARY KEY,
		party_type VARCHAR(20) NOT NULL,
		legal_name VARCHAR(255) NOT NULL,
		display_name VARCHAR(255),
		status VARCHAR(20) NOT NULL,
		external_reference VARCHAR(255),
		external_reference_type VARCHAR(50),
		attributes JSONB NOT NULL DEFAULT '[]'::jsonb,
		version BIGINT NOT NULL DEFAULT 1,
		created_at TIMESTAMP WITH TIME ZONE NOT NULL,
		updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
		deleted_at TIMESTAMP WITH TIME ZONE,
		created_by VARCHAR(255),
		updated_by VARCHAR(255),
		UNIQUE(external_reference, external_reference_type)
	)`, pq.QuoteIdentifier(schemaName))).Error)

	require.NoError(t, db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.audit_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		table_name VARCHAR(100) NOT NULL,
		operation VARCHAR(10) NOT NULL,
		record_id UUID NOT NULL,
		old_values TEXT,
		new_values TEXT,
		status VARCHAR(20) NOT NULL DEFAULT 'pending',
		created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT now(),
		retry_count INT NOT NULL DEFAULT 0,
		last_error TEXT,
		changed_by VARCHAR(100),
		transaction_id VARCHAR(100),
		client_ip VARCHAR(45),
		user_agent TEXT
	)`, pq.QuoteIdentifier(schemaName))).Error)

	// Create event_outbox in the tenant schema (search_path is tenant-only, no public fallback)
	require.NoError(t, db.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %s.event_outbox (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		event_type VARCHAR(200) NOT NULL,
		aggregate_id VARCHAR(100) NOT NULL,
		aggregate_type VARCHAR(100) NOT NULL,
		event_payload BYTEA NOT NULL,
		correlation_id VARCHAR(100),
		causation_id VARCHAR(100),
		status VARCHAR(20) NOT NULL,
		topic VARCHAR(200) NOT NULL,
		partition_key VARCHAR(200),
		created_at TIMESTAMPTZ NOT NULL,
		processed_at TIMESTAMPTZ,
		retry_count INTEGER NOT NULL DEFAULT 0,
		last_error TEXT,
		service_name VARCHAR(100) NOT NULL,
		tenant_id VARCHAR(100) NOT NULL
	)`, pq.QuoteIdentifier(schemaName))).Error)

	// Set search_path at the database level so all pool connections (including the outbox
	// worker's) resolve unqualified table names to the tenant schema. A session-level
	// SET search_path only affects a single pool connection; the load test needs all
	// connections to share the same schema routing.
	require.NoError(t, db.Exec(fmt.Sprintf("ALTER DATABASE test_db SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error)
	// Close idle connections so they reconnect and pick up the new database default.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxIdleConns(0) // closes idle connections immediately
	// Also set it on the current session for any in-flight use of this connection.
	require.NoError(t, db.Exec(fmt.Sprintf("SET search_path TO %s, public", pq.QuoteIdentifier(schemaName))).Error)

	ctx := tenant.WithTenant(bgCtx, tid)

	// Create party service with outbox publisher
	repo := persistence.NewRepository(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	outboxPublisher := events.NewOutboxPublisher("party")

	svc, err := NewService(repo, logger)
	require.NoError(t, err)
	svc.WithOutboxPublisher(outboxPublisher, db)

	// Create a thin KafkaPublisher adapter using a raw kgo.Client for test reliability.
	// The ProtoProducer enables idempotent writes which can cause metadata discovery
	// issues with single-broker Kafka testcontainers under load.
	kgoClient, err := kgo.NewClient(
		kgo.SeedBrokers(broker),
		kgo.AllowAutoTopicCreation(),
		kgo.DisableIdempotentWrite(),
		kgo.ProducerLinger(5*time.Millisecond),
	)
	require.NoError(t, err)
	producer := &testKafkaPublisher{client: kgoClient}

	outboxRepo := events.NewPostgresOutboxRepository(db)
	workerConfig := events.DefaultWorkerConfig("party")
	workerConfig.PollInterval = 200 * time.Millisecond // Fast polling for tests
	worker := events.NewWorker(outboxRepo, producer, workerConfig, logger)
	worker.Start(ctx)

	cleanup := func() {
		worker.Stop()
		kgoClient.Close()
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = kafkaContainer.Terminate(cleanupCtx)
		dbCleanup()
	}

	return &kafkaCascadeTestEnv{
		svc:        svc,
		db:         db,
		ctx:        ctx,
		broker:     broker,
		worker:     worker,
		cleanup:    cleanup,
		workerStop: worker.Stop,
	}
}

// createKafkaTopic creates a Kafka topic with 1 partition.
func createKafkaTopic(t *testing.T, broker, topic string) {
	t.Helper()

	client, err := kgo.NewClient(kgo.SeedBrokers(broker))
	require.NoError(t, err)
	defer client.Close()

	admin := kadm.NewClient(client)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := admin.CreateTopics(ctx, 1, 1, nil, topic)
	require.NoError(t, err)
	for _, r := range resp {
		if r.Err != nil && !errors.Is(r.Err, kerr.TopicAlreadyExists) {
			t.Fatalf("topic %q: %v (%s)", r.Topic, r.Err, r.ErrMessage)
		}
	}
}

// consumeKafkaEvents creates a consumer that collects events from a topic into a thread-safe slice.
// Returns a function to retrieve collected events and a cancel function.
func consumeKafkaEvents(t *testing.T, broker, topic, groupID string) (getEvents func() []*kgo.Record, cancel func()) {
	t.Helper()

	client, err := kgo.NewClient(
		kgo.SeedBrokers(broker),
		kgo.ConsumeTopics(topic),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	require.NoError(t, err)

	ctx, cancelFn := context.WithCancel(context.Background())

	var mu sync.Mutex
	var records []*kgo.Record

	go func() {
		for {
			fetches := client.PollFetches(ctx)
			if ctx.Err() != nil {
				return
			}
			fetches.EachRecord(func(r *kgo.Record) {
				mu.Lock()
				records = append(records, r)
				mu.Unlock()
			})
			client.AllowRebalance()
		}
	}()

	return func() []*kgo.Record {
			mu.Lock()
			defer mu.Unlock()
			out := make([]*kgo.Record, len(records))
			copy(out, records)
			return out
		}, func() {
			cancelFn()
			client.Close()
		}
}

// --- Kafka Cascade Integration Tests ---

// TestCascade_PartyTerminatedPublishesEvent verifies the full outbox-to-Kafka pipeline:
// 1. Register a party
// 2. ControlParty(TERMINATE) writes event to outbox
// 3. Outbox worker relays event to Kafka
// 4. Consumer receives PartyControlledEvent with correct fields
func TestCascade_PartyTerminatedPublishesEvent(t *testing.T) {
	env := setupKafkaCascadeTest(t)
	defer env.cleanup()

	// Start consumer before producing events
	getEvents, cancelConsumer := consumeKafkaEvents(t, env.broker, topics.PartyControlledV1, "test-terminated-"+uuid.New().String())
	defer cancelConsumer()

	// Register a party
	party := registerTestParty(t, env.ctx, env.svc, "Cascade Test Corp")
	require.NotEmpty(t, party.PartyId)

	// Terminate the party
	controlResp, err := env.svc.ControlParty(env.ctx, &pb.ControlPartyRequest{
		PartyId:       party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_TERMINATE,
		Reason:        "Business dissolution",
		ActorId:       "TEST-ACTOR-001",
	})
	require.NoError(t, err)
	assert.Equal(t, pb.PartyStatus_PARTY_STATUS_TERMINATED, controlResp.Party.Status)

	// Wait for the event to arrive on Kafka (outbox worker polls every 200ms)
	err = await.New().
		AtMost(10 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return len(getEvents()) >= 1
		})
	require.NoError(t, err, "PartyControlledEvent should arrive on Kafka within timeout")

	// Verify event content
	records := getEvents()
	require.Len(t, records, 1)

	var event eventsv1.PartyControlledEvent
	err = proto.Unmarshal(records[0].Value, &event)
	require.NoError(t, err)

	assert.Equal(t, party.PartyId, event.PartyId)
	assert.Equal(t, "TERMINATE", event.ControlAction)
	assert.Equal(t, "TERMINATED", event.NewStatus)
	assert.Equal(t, "Business dissolution", event.Reason)
	assert.Equal(t, "TEST-ACTOR-001", event.ActorId)
	assert.NotEmpty(t, event.EventId)
	assert.NotNil(t, event.Timestamp)

	// Verify Kafka headers contain event metadata
	headerMap := make(map[string]string)
	for _, h := range records[0].Headers {
		headerMap[h.Key] = string(h.Value)
	}
	assert.Equal(t, "party.controlled.v1", headerMap["event_type"])
	assert.Equal(t, "Party", headerMap["aggregate_type"])
	assert.Equal(t, party.PartyId, headerMap["aggregate_id"])
}

// TestCascade_EventReplayIdempotency verifies that duplicate events can be detected
// by consumers using event_id deduplication. The outbox guarantees at-least-once
// delivery, so consumers must handle replays.
func TestCascade_EventReplayIdempotency(t *testing.T) {
	env := setupKafkaCascadeTest(t)
	defer env.cleanup()

	// Start consumer
	getEvents, cancelConsumer := consumeKafkaEvents(t, env.broker, topics.PartyControlledV1, "test-idempotency-"+uuid.New().String())
	defer cancelConsumer()

	// Register and terminate a party
	party := registerTestParty(t, env.ctx, env.svc, "Idempotency Test Corp")
	_, err := env.svc.ControlParty(env.ctx, &pb.ControlPartyRequest{
		PartyId:       party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_TERMINATE,
		Reason:        "Idempotency test termination",
		ActorId:       "TEST-ACTOR-002",
	})
	require.NoError(t, err)

	// Wait for event
	err = await.New().
		AtMost(10 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return len(getEvents()) >= 1
		})
	require.NoError(t, err)

	records := getEvents()
	require.GreaterOrEqual(t, len(records), 1)

	// Parse the event to get its event_id
	var event eventsv1.PartyControlledEvent
	require.NoError(t, proto.Unmarshal(records[0].Value, &event))
	originalEventID := event.EventId

	// Simulate replay: manually re-publish the same raw record to Kafka
	replayClient, err := kgo.NewClient(kgo.SeedBrokers(env.broker))
	require.NoError(t, err)
	defer replayClient.Close()

	replayRecord := &kgo.Record{
		Topic:   topics.PartyControlledV1,
		Value:   records[0].Value,
		Headers: records[0].Headers,
	}
	results := replayClient.ProduceSync(context.Background(), replayRecord)
	require.NoError(t, results.FirstErr())

	// Wait for replay to be consumed
	err = await.New().
		AtMost(5 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return len(getEvents()) >= 2
		})
	require.NoError(t, err)

	// Consumer receives both records - verify they have the same event_id
	// so a consumer can deduplicate
	allRecords := getEvents()
	require.GreaterOrEqual(t, len(allRecords), 2)

	var replayedEvent eventsv1.PartyControlledEvent
	require.NoError(t, proto.Unmarshal(allRecords[1].Value, &replayedEvent))
	assert.Equal(t, originalEventID, replayedEvent.EventId,
		"Replayed event should have the same event_id for consumer-side deduplication")

	// Verify a consumer can deduplicate using event_id tracking
	seen := make(map[string]bool)
	uniqueCount := 0
	for _, r := range allRecords {
		var e eventsv1.PartyControlledEvent
		require.NoError(t, proto.Unmarshal(r.Value, &e))
		if !seen[e.EventId] {
			seen[e.EventId] = true
			uniqueCount++
		}
	}
	assert.Equal(t, 1, uniqueCount, "After deduplication, only 1 unique event should remain")
}

// TestCascade_PartialFailureRecovery verifies that events survive consumer restarts.
// The outbox writes events to Kafka, a consumer starts after the event is already on
// the topic, and still receives it (Kafka durability guarantee).
func TestCascade_PartialFailureRecovery(t *testing.T) {
	env := setupKafkaCascadeTest(t)
	defer env.cleanup()

	// Register and terminate a party (event published to Kafka via outbox)
	party := registerTestParty(t, env.ctx, env.svc, "Recovery Test Corp")
	_, err := env.svc.ControlParty(env.ctx, &pb.ControlPartyRequest{
		PartyId:       party.PartyId,
		ControlAction: pb.ControlAction_CONTROL_ACTION_TERMINATE,
		Reason:        "Recovery test termination",
		ActorId:       "TEST-ACTOR-003",
	})
	require.NoError(t, err)

	// Wait for the outbox worker to publish to Kafka (no consumer yet - simulating "service is down")
	// Use a temporary consumer to verify the event is on the topic
	tempGetEvents, tempCancel := consumeKafkaEvents(t, env.broker, topics.PartyControlledV1, "temp-verify-"+uuid.New().String())
	err = await.New().
		AtMost(10 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return len(tempGetEvents()) >= 1
		})
	require.NoError(t, err, "Event should be on Kafka before consumer restart")
	tempCancel()

	// Now start a NEW consumer (simulating "service restarted") with a fresh consumer group
	// that reads from the start
	getEvents, cancelConsumer := consumeKafkaEvents(t, env.broker, topics.PartyControlledV1, "recovered-consumer-"+uuid.New().String())
	defer cancelConsumer()

	// The new consumer should pick up the event
	err = await.New().
		AtMost(10 * time.Second).
		PollInterval(100 * time.Millisecond).
		Until(func() bool {
			return len(getEvents()) >= 1
		})
	require.NoError(t, err, "Recovered consumer should receive the event after restart")

	// Verify the event content is intact
	records := getEvents()
	require.GreaterOrEqual(t, len(records), 1)

	var event eventsv1.PartyControlledEvent
	require.NoError(t, proto.Unmarshal(records[0].Value, &event))
	assert.Equal(t, party.PartyId, event.PartyId)
	assert.Equal(t, "TERMINATE", event.ControlAction)
	assert.Equal(t, "TERMINATED", event.NewStatus)
}

// TestCascade_LoadTest100Terminations verifies that 100 concurrent party terminations
// all produce events that arrive on Kafka without loss or corruption.
func TestCascade_LoadTest100Terminations(t *testing.T) {
	env := setupKafkaCascadeTest(t)
	defer env.cleanup()

	const partyCount = 100

	// Start consumer
	getEvents, cancelConsumer := consumeKafkaEvents(t, env.broker, topics.PartyControlledV1, "test-load-"+uuid.New().String())
	defer cancelConsumer()

	// Register parties sequentially (DB constraint)
	parties := make([]*pb.Party, partyCount)
	for i := 0; i < partyCount; i++ {
		parties[i] = registerTestParty(t, env.ctx, env.svc, fmt.Sprintf("Load Test Corp %d", i))
	}

	// Terminate all concurrently with bounded parallelism to avoid DB connection exhaustion
	var wg sync.WaitGroup
	var failCount atomic.Int32
	sem := make(chan struct{}, 10) // limit to 10 concurrent terminations
	wg.Add(partyCount)
	for i := 0; i < partyCount; i++ {
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			_, err := env.svc.ControlParty(env.ctx, &pb.ControlPartyRequest{
				PartyId:       parties[idx].PartyId,
				ControlAction: pb.ControlAction_CONTROL_ACTION_TERMINATE,
				Reason:        fmt.Sprintf("Load test termination %d", idx),
				ActorId:       fmt.Sprintf("LOAD-ACTOR-%d", idx),
			})
			if err != nil {
				failCount.Add(1)
			}
		}(i)
	}
	wg.Wait()

	// Each termination targets a distinct party; failures should be zero.
	t.Logf("Terminations: %d succeeded, %d failed", partyCount-int(failCount.Load()), failCount.Load())
	require.Zero(t, failCount.Load(), "All terminations should succeed")

	// Wait for all events to arrive on Kafka
	err := await.New().
		AtMost(60 * time.Second).
		PollInterval(500 * time.Millisecond).
		Until(func() bool {
			got := len(getEvents())
			if got > 0 && got < partyCount {
				t.Logf("Kafka events received: %d/%d", got, partyCount)
			}
			return got >= partyCount
		})
	require.NoError(t, err, "All %d events should arrive on Kafka (got %d)", partyCount, len(getEvents()))

	// Verify no duplicate event_ids
	records := getEvents()
	eventIDs := make(map[string]bool)
	for _, r := range records {
		var event eventsv1.PartyControlledEvent
		require.NoError(t, proto.Unmarshal(r.Value, &event))
		assert.False(t, eventIDs[event.EventId], "Duplicate event_id detected: %s", event.EventId)
		eventIDs[event.EventId] = true
	}

	assert.Equal(t, partyCount, len(eventIDs),
		"Each termination should produce exactly one unique event")
}

// TestCascade_NegativeControlNonExistentParty verifies that controlling a non-existent
// party returns NotFound and does NOT publish any event to Kafka.
func TestCascade_NegativeControlNonExistentParty(t *testing.T) {
	env := setupKafkaCascadeTest(t)
	defer env.cleanup()

	// Start consumer
	getEvents, cancelConsumer := consumeKafkaEvents(t, env.broker, topics.PartyControlledV1, "test-negative-"+uuid.New().String())
	defer cancelConsumer()

	// Attempt to terminate a non-existent party
	nonExistentID := uuid.New().String()
	_, err := env.svc.ControlParty(env.ctx, &pb.ControlPartyRequest{
		PartyId:       nonExistentID,
		ControlAction: pb.ControlAction_CONTROL_ACTION_TERMINATE,
		Reason:        "Should not exist",
		ActorId:       "TEST-ACTOR-NEG",
	})
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.NotFound, st.Code())

	// Verify NO events are published by waiting for the outbox worker to process
	// any pending entries (polls every 200ms, so 2 seconds is sufficient).
	// Use await to confirm the condition stays false rather than time.Sleep.
	err = await.New().
		AtMost(2 * time.Second).
		PollInterval(200 * time.Millisecond).
		Until(func() bool {
			return len(getEvents()) > 0
		})
	require.Error(t, err, "No events should appear on Kafka for a non-existent party")
	records := getEvents()
	assert.Empty(t, records, "No event should be published for a non-existent party")

	// Also verify no event_outbox entries exist for this party
	var count int64
	result := env.db.Model(&events.EventOutbox{}).Where("aggregate_id = ?", nonExistentID).Count(&count)
	require.NoError(t, result.Error, "Outbox query should succeed")
	assert.Equal(t, int64(0), count, "No outbox entry should exist for non-existent party")
}
