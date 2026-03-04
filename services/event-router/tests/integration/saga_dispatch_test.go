//go:build integration

package integration_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	controlplanev1 "github.com/meridianhub/meridian/api/proto/meridian/control_plane/v1"
	"github.com/meridianhub/meridian/services/event-router/internal/handlers"
	"github.com/meridianhub/meridian/shared/platform/await"
)

// =============================================================================
// Happy Path Tests
// =============================================================================

// TestIntegration_HappyPath_EventTriggersMatchingSaga verifies the core
// event-to-saga dispatch flow: an event whose fields satisfy a CEL filter
// triggers the corresponding saga with correct input data.
func TestIntegration_HappyPath_EventTriggersMatchingSaga(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	filter := `event.amount > 100`
	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{
			Name:    "high_value_payment",
			Trigger: "event:payments",
			Filter:  &filter,
			Script:  "def run(ctx): pass",
		},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger, handlers.WithLogger(testLogger()))

	// Publish event with amount=500 (satisfies filter amount > 100)
	event := newTestEvent(t, map[string]any{"amount": 500.0, "currency": "GBP"})
	metadata := map[string]string{"x-correlation-id": "corr-happy-1"}

	err := h.Handle(t.Context(), "payments", event, metadata)
	require.NoError(t, err)

	calls := trigger.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "high_value_payment", calls[0].SagaName)
	assert.Equal(t, "corr-happy-1", calls[0].IdempotencyKey)

	// Verify input data contains event and metadata
	inputData := calls[0].InputData
	assert.Contains(t, inputData, "event")
	assert.Contains(t, inputData, "metadata")

	eventData, ok := inputData["event"].(map[string]any)
	require.True(t, ok, "event should be a map")
	assert.Equal(t, 500.0, eventData["amount"])
	assert.Equal(t, "GBP", eventData["currency"])
}

// TestIntegration_HappyPath_NoFilterAlwaysMatches verifies that a saga
// with no filter expression matches every event on its channel.
func TestIntegration_HappyPath_NoFilterAlwaysMatches(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{
			Name:    "catch_all_saga",
			Trigger: "event:orders",
			Script:  "def run(ctx): pass",
		},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger)

	event := newTestEvent(t, map[string]any{"order_id": "ord-1"})
	err := h.Handle(t.Context(), "orders", event, map[string]string{"x-correlation-id": "corr-no-filter"})

	require.NoError(t, err)
	require.Equal(t, 1, trigger.callCount())
	assert.Equal(t, "catch_all_saga", trigger.getCalls()[0].SagaName)
}

// =============================================================================
// Filter Rejection Tests
// =============================================================================

// TestIntegration_FilterRejection_NonMatchingEventSkipped verifies that an
// event whose fields do NOT satisfy the CEL filter is silently skipped.
func TestIntegration_FilterRejection_NonMatchingEventSkipped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	filter := `event.amount > 100`
	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{
			Name:    "high_value_payment",
			Trigger: "event:payments",
			Filter:  &filter,
			Script:  "def run(ctx): pass",
		},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger, handlers.WithLogger(testLogger()))

	// Publish event with amount=50 (does NOT satisfy filter amount > 100)
	event := newTestEvent(t, map[string]any{"amount": 50.0})
	err := h.Handle(t.Context(), "payments", event, map[string]string{"x-correlation-id": "corr-reject"})

	require.NoError(t, err)
	assert.Equal(t, 0, trigger.callCount(), "saga should NOT be triggered for non-matching event")
}

// TestIntegration_FilterRejection_MetadataFilter verifies filtering on
// metadata fields (not just event fields).
func TestIntegration_FilterRejection_MetadataFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	filter := `metadata.source == "billing"`
	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{
			Name:    "billing_saga",
			Trigger: "event:payments",
			Filter:  &filter,
			Script:  "def run(ctx): pass",
		},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger)

	t.Run("matching metadata triggers saga", func(t *testing.T) {
		event := newTestEvent(t, map[string]any{"amount": 100.0})
		err := h.Handle(t.Context(), "payments", event, map[string]string{
			"x-correlation-id": "corr-meta-match",
			"source":           "billing",
		})
		require.NoError(t, err)
		assert.Equal(t, 1, trigger.callCount())
	})

	t.Run("non-matching metadata skips saga", func(t *testing.T) {
		prevCount := trigger.callCount()
		event := newTestEvent(t, map[string]any{"amount": 100.0})
		err := h.Handle(t.Context(), "payments", event, map[string]string{
			"x-correlation-id": "corr-meta-no-match",
			"source":           "manual",
		})
		require.NoError(t, err)
		assert.Equal(t, prevCount, trigger.callCount(), "saga should NOT be triggered for non-matching metadata")
	})
}

// =============================================================================
// Idempotency Tests (with CockroachDB)
// =============================================================================

// TestIntegration_Idempotency_DuplicateEventSkipped verifies that the same
// event published twice results in only one saga execution when idempotency
// is enabled.
func TestIntegration_Idempotency_DuplicateEventSkipped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := requireCockroachDB(t)
	store := newTestIdempotencyStore(t, pool)

	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{
			Name:    "idempotent_saga",
			Trigger: "event:orders",
			Script:  "def run(ctx): pass",
		},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger,
		handlers.WithIdempotencyStore(store),
		handlers.WithLogger(testLogger()),
	)

	event := newTestEvent(t, map[string]any{"order_id": "ord-dup"})
	metadata := map[string]string{"x-correlation-id": "corr-dup-1"}

	// First call: should trigger
	err := h.Handle(t.Context(), "orders", event, metadata)
	require.NoError(t, err)
	assert.Equal(t, 1, trigger.callCount(), "first call should trigger saga")

	// Second call with same correlation ID: should be deduplicated
	err = h.Handle(t.Context(), "orders", event, metadata)
	require.NoError(t, err)
	assert.Equal(t, 1, trigger.callCount(), "second call should be deduplicated")
}

// TestIntegration_Idempotency_DifferentCorrelationIDsExecuteBoth verifies that
// events with different correlation IDs are treated independently.
func TestIntegration_Idempotency_DifferentCorrelationIDsExecuteBoth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	pool := requireCockroachDB(t)
	store := newTestIdempotencyStore(t, pool)

	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{
			Name:    "unique_saga",
			Trigger: "event:orders",
			Script:  "def run(ctx): pass",
		},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger,
		handlers.WithIdempotencyStore(store),
		handlers.WithLogger(testLogger()),
	)

	event := newTestEvent(t, map[string]any{"order_id": "ord-unique"})

	err := h.Handle(t.Context(), "orders", event, map[string]string{"x-correlation-id": "corr-a"})
	require.NoError(t, err)

	err = h.Handle(t.Context(), "orders", event, map[string]string{"x-correlation-id": "corr-b"})
	require.NoError(t, err)

	assert.Equal(t, 2, trigger.callCount(), "different correlation IDs should each trigger")
}

// =============================================================================
// Chain Depth Tests
// =============================================================================

// TestIntegration_ChainDepth_ExceededDropsEvent verifies that events with a
// chain depth at or above the configured maximum are silently dropped.
func TestIntegration_ChainDepth_ExceededDropsEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{
			Name:    "chain_saga",
			Trigger: "event:orders",
			Script:  "def run(ctx): pass",
		},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger,
		handlers.WithMaxChainDepth(3),
		handlers.WithLogger(testLogger()),
	)

	event := newTestEvent(t, map[string]any{"order_id": "ord-chain"})

	t.Run("depth below limit triggers", func(t *testing.T) {
		err := h.Handle(t.Context(), "orders", event, map[string]string{
			"x-correlation-id":       "corr-chain-ok",
			"x-meridian-chain-depth": "2",
		})
		require.NoError(t, err)
		assert.Equal(t, 1, trigger.callCount())
	})

	t.Run("depth at limit drops", func(t *testing.T) {
		prevCount := trigger.callCount()
		err := h.Handle(t.Context(), "orders", event, map[string]string{
			"x-correlation-id":       "corr-chain-at",
			"x-meridian-chain-depth": "3",
		})
		require.NoError(t, err)
		assert.Equal(t, prevCount, trigger.callCount(), "event at max depth should be dropped")
	})

	t.Run("depth above limit drops", func(t *testing.T) {
		prevCount := trigger.callCount()
		err := h.Handle(t.Context(), "orders", event, map[string]string{
			"x-correlation-id":       "corr-chain-above",
			"x-meridian-chain-depth": "10",
		})
		require.NoError(t, err)
		assert.Equal(t, prevCount, trigger.callCount(), "event above max depth should be dropped")
	})
}

// =============================================================================
// Multi-Saga Concurrent Dispatch Tests
// =============================================================================

// TestIntegration_MultiSaga_AllMatchingExecute verifies that when multiple
// sagas are registered on the same channel with different filters, an event
// matching all of them triggers all of them.
func TestIntegration_MultiSaga_AllMatchingExecute(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	filter1 := `event.amount > 0`
	filter2 := `event.amount > 100`
	filter3 := `event.amount > 200`

	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{Name: "low_threshold", Trigger: "event:payments", Filter: &filter1, Script: "def run(ctx): pass"},
		{Name: "mid_threshold", Trigger: "event:payments", Filter: &filter2, Script: "def run(ctx): pass"},
		{Name: "high_threshold", Trigger: "event:payments", Filter: &filter3, Script: "def run(ctx): pass"},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger, handlers.WithLogger(testLogger()))

	// amount=500 matches all three filters
	event := newTestEvent(t, map[string]any{"amount": 500.0})
	err := h.Handle(t.Context(), "payments", event, map[string]string{"x-correlation-id": "corr-multi-all"})

	require.NoError(t, err)
	calls := trigger.getCalls()
	require.Len(t, calls, 3, "all three sagas should be triggered")

	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = c.SagaName
	}
	assert.Contains(t, names, "low_threshold")
	assert.Contains(t, names, "mid_threshold")
	assert.Contains(t, names, "high_threshold")
}

// TestIntegration_MultiSaga_PartialMatch verifies that when multiple sagas
// are registered on the same channel, only those whose filters match are triggered.
func TestIntegration_MultiSaga_PartialMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	filter1 := `event.amount > 0`
	filter2 := `event.amount > 100`
	filter3 := `event.amount > 1000`

	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{Name: "low_threshold", Trigger: "event:payments", Filter: &filter1, Script: "def run(ctx): pass"},
		{Name: "mid_threshold", Trigger: "event:payments", Filter: &filter2, Script: "def run(ctx): pass"},
		{Name: "high_threshold", Trigger: "event:payments", Filter: &filter3, Script: "def run(ctx): pass"},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger, handlers.WithLogger(testLogger()))

	// amount=150 matches low and mid, but NOT high
	event := newTestEvent(t, map[string]any{"amount": 150.0})
	err := h.Handle(t.Context(), "payments", event, map[string]string{"x-correlation-id": "corr-multi-partial"})

	require.NoError(t, err)
	calls := trigger.getCalls()
	require.Len(t, calls, 2, "only two sagas should be triggered")

	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = c.SagaName
	}
	assert.Contains(t, names, "low_threshold")
	assert.Contains(t, names, "mid_threshold")
	assert.NotContains(t, names, "high_threshold")
}

// =============================================================================
// E2E Kafka Integration Test
// =============================================================================

// TestIntegration_E2E_KafkaEventDispatch verifies the full event-to-saga flow
// with a real Kafka container: publish an event to Kafka, consume it, evaluate
// the CEL filter, and trigger the matching saga.
func TestIntegration_E2E_KafkaEventDispatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	broker := requireKafka(t)
	pool := requireCockroachDB(t)

	const topic = "e2e-saga-dispatch"
	createTopic(t, broker, topic)

	store := newTestIdempotencyStore(t, pool)

	filter := `event.amount > 100`
	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{
			Name:    "e2e_payment_saga",
			Trigger: "event:" + topic,
			Filter:  &filter,
			Script:  "def run(ctx): pass",
		},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger,
		handlers.WithIdempotencyStore(store),
		handlers.WithLogger(testLogger()),
	)

	// Consume from Kafka and dispatch through handler
	consumeAndDispatch(t, broker, topic, h)

	// Publish an event with amount=500
	publishEvent(t, broker, topic,
		map[string]any{"amount": 500.0, "currency": "USD"},
		map[string]string{"x-correlation-id": "corr-e2e-1"},
	)

	// Wait for the saga to be triggered
	err := await.New().
		AtMost(30 * secondDuration).
		PollInterval(200 * millisecondDuration).
		Until(func() bool {
			return trigger.callCount() >= 1
		})
	require.NoError(t, err, "saga should be triggered within timeout")

	calls := trigger.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "e2e_payment_saga", calls[0].SagaName)
}

// TestIntegration_E2E_KafkaFilterRejection verifies that events not matching
// the CEL filter are consumed from Kafka but do NOT trigger a saga.
func TestIntegration_E2E_KafkaFilterRejection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	broker := requireKafka(t)

	const topic = "e2e-saga-filter-reject"
	createTopic(t, broker, topic)

	filter := `event.amount > 100`
	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{
			Name:    "filtered_saga",
			Trigger: "event:" + topic,
			Filter:  &filter,
			Script:  "def run(ctx): pass",
		},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger, handlers.WithLogger(testLogger()))

	consumeAndDispatch(t, broker, topic, h)

	// Publish event with amount=50 (does NOT satisfy filter)
	publishEvent(t, broker, topic,
		map[string]any{"amount": 50.0},
		map[string]string{"x-correlation-id": "corr-e2e-reject"},
	)

	// Publish a second event that DOES match to prove the consumer is working
	publishEvent(t, broker, topic,
		map[string]any{"amount": 200.0},
		map[string]string{"x-correlation-id": "corr-e2e-match"},
	)

	// Wait for the matching event to be processed
	err := await.New().
		AtMost(30 * secondDuration).
		PollInterval(200 * millisecondDuration).
		Until(func() bool {
			return trigger.callCount() >= 1
		})
	require.NoError(t, err, "matching saga should be triggered")

	// Only the matching event should have triggered
	calls := trigger.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "filtered_saga", calls[0].SagaName)
	assert.Equal(t, "corr-e2e-match", calls[0].IdempotencyKey)
}

// TestIntegration_E2E_KafkaIdempotency verifies that the same event produced
// twice to Kafka results in only one saga execution with idempotency enabled.
func TestIntegration_E2E_KafkaIdempotency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	broker := requireKafka(t)
	pool := requireCockroachDB(t)

	const topic = "e2e-saga-idempotent"
	createTopic(t, broker, topic)

	store := newTestIdempotencyStore(t, pool)

	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{
			Name:    "idempotent_e2e_saga",
			Trigger: "event:" + topic,
			Script:  "def run(ctx): pass",
		},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger,
		handlers.WithIdempotencyStore(store),
		handlers.WithLogger(testLogger()),
	)

	consumeAndDispatch(t, broker, topic, h)

	// Publish the SAME event twice with the same correlation ID
	for i := 0; i < 2; i++ {
		publishEvent(t, broker, topic,
			map[string]any{"order_id": "ord-e2e-dup"},
			map[string]string{"x-correlation-id": "corr-e2e-dup"},
		)
	}

	// Wait for processing to stabilize. We expect exactly 1 saga trigger.
	// First, wait for at least one trigger.
	err := await.New().
		AtMost(30 * secondDuration).
		PollInterval(200 * millisecondDuration).
		Until(func() bool {
			return trigger.callCount() >= 1
		})
	require.NoError(t, err, "at least one saga trigger expected")

	// Verify deduplication: poll for a second trigger that should never arrive.
	// We intentionally wait 3 seconds and assert that the poll times out. This is
	// NOT a flaky timing hack -- the idempotency store enforces deduplication at the
	// database level (CockroachDB unique constraint on saga_name + correlation_id),
	// so a second trigger is structurally impossible once the first is recorded.
	// The short timeout simply proves no duplicate leaked through.
	err = await.New().
		AtMost(3 * secondDuration).
		PollInterval(200 * millisecondDuration).
		Until(func() bool {
			return trigger.callCount() >= 2
		})
	assert.ErrorIs(t, err, await.ErrTimeout, "second event should be deduplicated")
	assert.Equal(t, 1, trigger.callCount(), "only one saga execution expected with idempotency")
}

// TestIntegration_E2E_KafkaMultiSagaDispatch verifies that when multiple sagas
// are registered for the same Kafka topic with different filters, an event
// triggers only the matching ones.
func TestIntegration_E2E_KafkaMultiSagaDispatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	broker := requireKafka(t)

	const topic = "e2e-saga-multi"
	createTopic(t, broker, topic)

	filter1 := `event.amount > 0`
	filter2 := `event.amount > 100`
	filter3 := `event.amount > 1000`

	reg := newTestRegistry(t, []*controlplanev1.SagaDefinition{
		{Name: "saga_low", Trigger: "event:" + topic, Filter: &filter1, Script: "def run(ctx): pass"},
		{Name: "saga_mid", Trigger: "event:" + topic, Filter: &filter2, Script: "def run(ctx): pass"},
		{Name: "saga_high", Trigger: "event:" + topic, Filter: &filter3, Script: "def run(ctx): pass"},
	})

	trigger := &recordingSagaTrigger{}
	h := newTestHandler(reg, trigger, handlers.WithLogger(testLogger()))

	consumeAndDispatch(t, broker, topic, h)

	// amount=500 matches saga_low and saga_mid but NOT saga_high
	publishEvent(t, broker, topic,
		map[string]any{"amount": 500.0},
		map[string]string{"x-correlation-id": "corr-e2e-multi"},
	)

	// Wait for 2 triggers
	err := await.New().
		AtMost(30 * secondDuration).
		PollInterval(200 * millisecondDuration).
		Until(func() bool {
			return trigger.callCount() >= 2
		})
	require.NoError(t, err, "two sagas should be triggered")

	// Verify only 2 (not 3) sagas triggered
	calls := trigger.getCalls()
	require.Len(t, calls, 2)

	names := make([]string, len(calls))
	for i, c := range calls {
		names[i] = c.SagaName
	}
	assert.Contains(t, names, "saga_low")
	assert.Contains(t, names, "saga_mid")
	assert.NotContains(t, names, "saga_high")
}
