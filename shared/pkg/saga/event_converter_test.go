package saga

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	eventsv1 "github.com/meridianhub/meridian/api/proto/meridian/events/v1"
)

// TestEventToInputData_BasicConversion verifies basic proto to input_data map conversion
// with snake_case field names (UseProtoNames: true).
func TestEventToInputData_BasicConversion(t *testing.T) {
	ts := timestamppb.New(time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC))
	event := &eventsv1.TransactionCapturedEvent{
		LogId:          "550e8400-e29b-41d4-a716-446655440000",
		AccountId:      "acc-123",
		TransactionId:  "550e8400-e29b-41d4-a716-446655440001",
		AmountCents:    10000,
		InstrumentCode: "GBP",
		Direction:      "CREDIT",
		Source:         "MANUAL",
		CorrelationId:  "corr-456",
		Timestamp:      ts,
		Version:        1,
	}
	metadata := map[string]string{
		"tenant_id": "tenant-abc",
		"source":    "kafka",
	}

	result, err := EventToInputData(event, metadata)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Top-level keys
	assert.Contains(t, result, "event")
	assert.Contains(t, result, "metadata")

	eventMap, ok := result["event"].(map[string]any)
	require.True(t, ok, "event should be map[string]any")

	// snake_case field names (UseProtoNames: true)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", eventMap["log_id"])
	assert.Equal(t, "acc-123", eventMap["account_id"])
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440001", eventMap["transaction_id"])
	assert.Equal(t, "GBP", eventMap["instrument_code"])
	assert.Equal(t, "CREDIT", eventMap["direction"])
	assert.Equal(t, "MANUAL", eventMap["source"])
	assert.Equal(t, "corr-456", eventMap["correlation_id"])

	// Numeric fields (JSON numbers)
	amountCents, ok := eventMap["amount_cents"]
	require.True(t, ok, "amount_cents should be present")
	assert.NotNil(t, amountCents)

	// Metadata preserved
	metadataMap, ok := result["metadata"].(map[string]string)
	require.True(t, ok, "metadata should be map[string]string")
	assert.Equal(t, "tenant-abc", metadataMap["tenant_id"])
	assert.Equal(t, "kafka", metadataMap["source"])
}

// TestEventToInputData_SnakeCaseFieldNames verifies that proto field names are used
// (not camelCase JSON names), matching CEL filter and Starlark script expectations.
func TestEventToInputData_SnakeCaseFieldNames(t *testing.T) {
	ts := timestamppb.Now()
	event := &eventsv1.TransactionCapturedEvent{
		LogId:          "550e8400-e29b-41d4-a716-446655440000",
		AccountId:      "acc-123",
		TransactionId:  "550e8400-e29b-41d4-a716-446655440001",
		AmountCents:    5000,
		InstrumentCode: "USD",
		Direction:      "DEBIT",
		Source:         "AUTOMATED",
		CorrelationId:  "corr-789",
		Timestamp:      ts,
		Version:        2,
	}

	result, err := EventToInputData(event, nil)
	require.NoError(t, err)

	eventMap := result["event"].(map[string]any)

	// Verify snake_case names, not camelCase (logId, accountId, etc.)
	assert.Contains(t, eventMap, "log_id", "should use snake_case log_id not camelCase logId")
	assert.Contains(t, eventMap, "account_id", "should use snake_case account_id not camelCase accountId")
	assert.Contains(t, eventMap, "transaction_id", "should use snake_case transaction_id not camelCase transactionId")
	assert.Contains(t, eventMap, "amount_cents", "should use snake_case amount_cents not camelCase amountCents")
	assert.Contains(t, eventMap, "instrument_code", "should use snake_case instrument_code not camelCase instrumentCode")
	assert.Contains(t, eventMap, "correlation_id", "should use snake_case correlation_id not camelCase correlationId")

	assert.NotContains(t, eventMap, "logId")
	assert.NotContains(t, eventMap, "accountId")
	assert.NotContains(t, eventMap, "transactionId")
	assert.NotContains(t, eventMap, "amountCents")
	assert.NotContains(t, eventMap, "instrumentCode")
}

// TestEventToInputData_MetadataIncluded verifies metadata map is accessible at top level.
func TestEventToInputData_MetadataIncluded(t *testing.T) {
	ts := timestamppb.Now()
	event := &eventsv1.TransactionCapturedEvent{
		LogId:         "550e8400-e29b-41d4-a716-446655440000",
		AccountId:     "acc-123",
		TransactionId: "550e8400-e29b-41d4-a716-446655440001",
		Direction:     "CREDIT",
		Source:        "MANUAL",
		CorrelationId: "corr-456",
		Timestamp:     ts,
		Version:       1,
	}
	metadata := map[string]string{
		"event_type":  "position_keeping.transaction_captured",
		"kafka_topic": "meridian.events.position_keeping",
		"partition":   "3",
		"offset":      "42",
	}

	result, err := EventToInputData(event, metadata)
	require.NoError(t, err)

	metadataMap, ok := result["metadata"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "position_keeping.transaction_captured", metadataMap["event_type"])
	assert.Equal(t, "meridian.events.position_keeping", metadataMap["kafka_topic"])
	assert.Equal(t, "3", metadataMap["partition"])
	assert.Equal(t, "42", metadataMap["offset"])
}

// TestEventToInputData_NilMetadata verifies nil metadata is handled gracefully.
func TestEventToInputData_NilMetadata(t *testing.T) {
	ts := timestamppb.Now()
	event := &eventsv1.TransactionCapturedEvent{
		LogId:         "550e8400-e29b-41d4-a716-446655440000",
		AccountId:     "acc-123",
		TransactionId: "550e8400-e29b-41d4-a716-446655440001",
		Direction:     "CREDIT",
		Source:        "MANUAL",
		CorrelationId: "corr-456",
		Timestamp:     ts,
		Version:       1,
	}

	result, err := EventToInputData(event, nil)
	require.NoError(t, err)
	require.NotNil(t, result)

	assert.Contains(t, result, "event")
	assert.Contains(t, result, "metadata")

	// metadata should be nil (not omitted)
	assert.Nil(t, result["metadata"])
}

// TestEventToInputData_TimestampField verifies that Timestamp fields are serialized.
func TestEventToInputData_TimestampField(t *testing.T) {
	fixedTime := time.Date(2024, 6, 15, 12, 30, 0, 0, time.UTC)
	ts := timestamppb.New(fixedTime)
	event := &eventsv1.TransactionCapturedEvent{
		LogId:         "550e8400-e29b-41d4-a716-446655440000",
		AccountId:     "acc-123",
		TransactionId: "550e8400-e29b-41d4-a716-446655440001",
		Direction:     "CREDIT",
		Source:        "MANUAL",
		CorrelationId: "corr-456",
		Timestamp:     ts,
		Version:       1,
	}

	result, err := EventToInputData(event, nil)
	require.NoError(t, err)

	eventMap := result["event"].(map[string]any)

	// Timestamp should be present as a string (RFC3339 format from protojson)
	timestamp, ok := eventMap["timestamp"]
	require.True(t, ok, "timestamp field should be present")
	assert.NotNil(t, timestamp)

	tsStr, ok := timestamp.(string)
	require.True(t, ok, "timestamp should be a string in RFC3339 format")
	assert.Contains(t, tsStr, "2024-06-15")
}

// TestEventToInputData_RepeatedFields verifies repeated fields become arrays.
func TestEventToInputData_RepeatedFields(t *testing.T) {
	ts := timestamppb.Now()
	event := &eventsv1.BulkTransactionCapturedEvent{
		BatchId:          "550e8400-e29b-41d4-a716-446655440000",
		TransactionCount: 3,
		LogIds: []string{
			"550e8400-e29b-41d4-a716-446655440001",
			"550e8400-e29b-41d4-a716-446655440002",
			"550e8400-e29b-41d4-a716-446655440003",
		},
		Source:        "IMPORTED",
		CorrelationId: "corr-bulk-123",
		Timestamp:     ts,
		Version:       1,
	}

	result, err := EventToInputData(event, nil)
	require.NoError(t, err)

	eventMap := result["event"].(map[string]any)

	logIDs, ok := eventMap["log_ids"]
	require.True(t, ok, "log_ids (repeated field) should be present")

	logIDSlice, ok := logIDs.([]any)
	require.True(t, ok, "repeated field should be a slice")
	assert.Len(t, logIDSlice, 3)
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440001", logIDSlice[0])
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440002", logIDSlice[1])
	assert.Equal(t, "550e8400-e29b-41d4-a716-446655440003", logIDSlice[2])
}

// TestEventToInputData_DifferentEventTypes verifies conversion works with multiple event types.
func TestEventToInputData_DifferentEventTypes(t *testing.T) {
	ts := timestamppb.Now()

	t.Run("TransactionReconciledEvent", func(t *testing.T) {
		event := &eventsv1.TransactionReconciledEvent{
			LogId:                "550e8400-e29b-41d4-a716-446655440000",
			AccountId:            "acc-456",
			ReconciliationStatus: "auto_reconciled",
			Reason:               "matched external record",
			ReconciledBy:         "system",
			CorrelationId:        "corr-rec-123",
			Timestamp:            ts,
			Version:              1,
		}

		result, err := EventToInputData(event, map[string]string{"type": "reconciled"})
		require.NoError(t, err)

		eventMap := result["event"].(map[string]any)
		assert.Equal(t, "550e8400-e29b-41d4-a716-446655440000", eventMap["log_id"])
		assert.Equal(t, "auto_reconciled", eventMap["reconciliation_status"])
		assert.Equal(t, "matched external record", eventMap["reason"])
	})

	t.Run("TransactionPostedEvent", func(t *testing.T) {
		event := &eventsv1.TransactionPostedEvent{
			LogId:            "550e8400-e29b-41d4-a716-446655440000",
			AccountId:        "acc-789",
			PostingReference: "REF-001",
			Reason:           "end of day posting",
			PostedBy:         "posting_system",
			CorrelationId:    "corr-post-456",
			Timestamp:        ts,
			Version:          1,
		}

		result, err := EventToInputData(event, nil)
		require.NoError(t, err)

		eventMap := result["event"].(map[string]any)
		assert.Equal(t, "REF-001", eventMap["posting_reference"])
		assert.Equal(t, "posting_system", eventMap["posted_by"])
	})
}

// TestEventToInputData_ZeroValuesOmitted verifies zero/empty values are omitted
// (EmitUnpopulated: false behavior).
func TestEventToInputData_ZeroValuesOmitted(t *testing.T) {
	ts := timestamppb.Now()
	// Create event with only required fields set, leaving optional fields at zero value
	event := &eventsv1.TransactionCapturedEvent{
		LogId:         "550e8400-e29b-41d4-a716-446655440000",
		AccountId:     "acc-123",
		TransactionId: "550e8400-e29b-41d4-a716-446655440001",
		Direction:     "CREDIT",
		Source:        "MANUAL",
		CorrelationId: "corr-456",
		Timestamp:     ts,
		Version:       1,
		// Description, Reference are empty strings - should be omitted
		// AmountCents is 0 - should be omitted
	}

	result, err := EventToInputData(event, nil)
	require.NoError(t, err)

	eventMap := result["event"].(map[string]any)

	// Zero-value fields should be omitted (EmitUnpopulated: false)
	_, hasDescription := eventMap["description"]
	assert.False(t, hasDescription, "empty description should be omitted")

	_, hasReference := eventMap["reference"]
	assert.False(t, hasReference, "empty reference should be omitted")
}

// TestEventToInputData_NilEvent verifies nil event returns ErrNilEvent.
func TestEventToInputData_NilEvent(t *testing.T) {
	_, err := EventToInputData(nil, nil)
	require.ErrorIs(t, err, ErrNilEvent)
}

// TestEventToInputData_CELCompatibility verifies the output structure matches
// what CEL filters expect: event.field_name notation.
func TestEventToInputData_CELCompatibility(t *testing.T) {
	ts := timestamppb.Now()
	event := &eventsv1.TransactionCapturedEvent{
		LogId:          "550e8400-e29b-41d4-a716-446655440000",
		AccountId:      "acc-cel-test",
		TransactionId:  "550e8400-e29b-41d4-a716-446655440001",
		AmountCents:    50000,
		InstrumentCode: "GBP",
		Direction:      "CREDIT",
		Source:         "MANUAL",
		CorrelationId:  "corr-cel-123",
		Timestamp:      ts,
		Version:        1,
	}

	result, err := EventToInputData(event, map[string]string{"tenant_id": "t-123"})
	require.NoError(t, err)

	// Verify the structure can be passed directly to CEL evaluator
	// CEL expressions like: event.account_id == "acc-cel-test"
	eventMap, ok := result["event"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "acc-cel-test", eventMap["account_id"])

	// CEL expressions like: metadata.tenant_id == "t-123"
	metadataMap, ok := result["metadata"].(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "t-123", metadataMap["tenant_id"])
}

// TestEventToInputData_StarlarkCompatibility verifies the output can be used
// in Starlark scripts: input_data["event"]["field_name"].
func TestEventToInputData_StarlarkCompatibility(t *testing.T) {
	ts := timestamppb.Now()
	event := &eventsv1.TransactionCapturedEvent{
		LogId:          "550e8400-e29b-41d4-a716-446655440000",
		AccountId:      "acc-starlark-test",
		TransactionId:  "550e8400-e29b-41d4-a716-446655440001",
		AmountCents:    75000,
		InstrumentCode: "EUR",
		Direction:      "DEBIT",
		Source:         "AUTOMATED",
		CorrelationId:  "corr-starlark-789",
		Timestamp:      ts,
		Version:        3,
	}

	result, err := EventToInputData(event, map[string]string{
		"saga_trigger": "event.position_keeping.transaction_captured",
	})
	require.NoError(t, err)

	// Simulate Starlark access: input_data["event"]["account_id"]
	eventMapRaw := result["event"]
	require.NotNil(t, eventMapRaw)

	eventMap, ok := eventMapRaw.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "acc-starlark-test", eventMap["account_id"])
	assert.Equal(t, "EUR", eventMap["instrument_code"])

	// Simulate: input_data["metadata"]["saga_trigger"]
	metadataRaw := result["metadata"]
	require.NotNil(t, metadataRaw)

	metadataMap, ok := metadataRaw.(map[string]string)
	require.True(t, ok)
	assert.Equal(t, "event.position_keeping.transaction_captured", metadataMap["saga_trigger"])
}
