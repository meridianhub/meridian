package eventstream_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ParseClientMessage ---

func TestParseClientMessage_Subscribe(t *testing.T) {
	raw := `{
		"type": "subscribe",
		"id": "req-001",
		"channels": ["payment-order.*", "position-keeping.*"],
		"filters": {
			"aggregate_id": "agg-123",
			"correlation_id": "corr-456"
		}
	}`

	msg, err := eventstream.ParseClientMessage([]byte(raw))
	require.NoError(t, err)
	assert.Equal(t, eventstream.ClientMessageTypeSubscribe, msg.Type)
	assert.Equal(t, "req-001", msg.ID)
	assert.Equal(t, []eventstream.ChannelPattern{"payment-order.*", "position-keeping.*"}, msg.Channels)
	assert.Equal(t, "agg-123", msg.Filters.AggregateID)
	assert.Equal(t, "corr-456", msg.Filters.CorrelationID)
}

func TestParseClientMessage_Unsubscribe(t *testing.T) {
	raw := `{"type": "unsubscribe", "id": "req-002"}`

	msg, err := eventstream.ParseClientMessage([]byte(raw))
	require.NoError(t, err)
	assert.Equal(t, eventstream.ClientMessageTypeUnsubscribe, msg.Type)
	assert.Equal(t, "req-002", msg.ID)
	assert.Empty(t, msg.Channels)
}

func TestParseClientMessage_MissingOptionalFields(t *testing.T) {
	raw := `{"type": "subscribe", "id": "req-003", "channels": ["*"]}`

	msg, err := eventstream.ParseClientMessage([]byte(raw))
	require.NoError(t, err)
	assert.Equal(t, eventstream.ClientMessageTypeSubscribe, msg.Type)
	assert.Equal(t, []eventstream.ChannelPattern{"*"}, msg.Channels)
	assert.Empty(t, msg.Filters.AggregateID)
	assert.Empty(t, msg.Filters.CorrelationID)
}

func TestParseClientMessage_MalformedJSON(t *testing.T) {
	_, err := eventstream.ParseClientMessage([]byte(`{not valid json}`))
	require.Error(t, err)
}

func TestParseClientMessage_EmptyInput(t *testing.T) {
	_, err := eventstream.ParseClientMessage([]byte(``))
	require.Error(t, err)
}

func TestParseClientMessage_NullInput(t *testing.T) {
	_, err := eventstream.ParseClientMessage(nil)
	require.Error(t, err)
}

func TestParseClientMessage_JSONRoundTrip(t *testing.T) {
	original := eventstream.ClientMessage{
		Type:     eventstream.ClientMessageTypeSubscribe,
		ID:       "req-999",
		Channels: []eventstream.ChannelPattern{"payment-order.*"},
		Filters: eventstream.SubscriptionFilters{
			AggregateID:   "agg-1",
			CorrelationID: "corr-1",
		},
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	parsed, err := eventstream.ParseClientMessage(data)
	require.NoError(t, err)
	assert.Equal(t, original.Type, parsed.Type)
	assert.Equal(t, original.ID, parsed.ID)
	assert.Equal(t, original.Channels, parsed.Channels)
	assert.Equal(t, original.Filters, parsed.Filters)
}

// --- ServerMessage.Serialize ---

func TestServerMessage_Serialize_EventMessage(t *testing.T) {
	ts := time.Date(2026, 2, 22, 12, 0, 0, 0, time.UTC)
	payload := json.RawMessage(`{"amount":"100.00"}`)

	msg := eventstream.ServerMessage{
		Type:           eventstream.ServerMessageTypeEvent,
		SubscriptionID: "sub-001",
		Channel:        "payment-order.created",
		Event: &eventstream.EventPayload{
			EventID:       "evt-123",
			EventType:     "payment_order.created.v1",
			AggregateID:   "agg-456",
			AggregateType: "PaymentOrder",
			TenantID:      "tenant-abc",
			CorrelationID: "corr-789",
			CausationID:   "cause-000",
			Timestamp:     ts,
			Payload:       payload,
		},
	}

	data, err := msg.Serialize()
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Equal(t, "event", raw["type"])
	assert.Equal(t, "sub-001", raw["subscription_id"])
	assert.Equal(t, "payment-order.created", raw["channel"])

	event, ok := raw["event"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "evt-123", event["event_id"])
	assert.Equal(t, "payment_order.created.v1", event["event_type"])
	assert.Equal(t, "agg-456", event["aggregate_id"])
	assert.Equal(t, "PaymentOrder", event["aggregate_type"])
	assert.Equal(t, "tenant-abc", event["tenant_id"])
	assert.Equal(t, "corr-789", event["correlation_id"])
	assert.Equal(t, "cause-000", event["causation_id"])

	// Timestamp must be ISO 8601
	tsStr, ok := event["timestamp"].(string)
	require.True(t, ok)
	assert.Equal(t, "2026-02-22T12:00:00Z", tsStr)
}

func TestServerMessage_Serialize_SubscribedMessage(t *testing.T) {
	msg := eventstream.ServerMessage{
		Type:           eventstream.ServerMessageTypeSubscribed,
		SubscriptionID: "sub-001",
	}

	data, err := msg.Serialize()
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Equal(t, "subscribed", raw["type"])
	assert.Equal(t, "sub-001", raw["subscription_id"])
	_, hasEvent := raw["event"]
	assert.False(t, hasEvent, "subscribed message should not include event field")
	_, hasError := raw["error"]
	assert.False(t, hasError, "subscribed message should not include error field")
}

func TestServerMessage_Serialize_ErrorMessage(t *testing.T) {
	msg := eventstream.ServerMessage{
		Type:         eventstream.ServerMessageTypeError,
		ErrorCode:    eventstream.ErrorCodeInvalidChannel,
		ErrorMessage: "channel pattern 'foo*bar' is invalid",
	}

	data, err := msg.Serialize()
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Equal(t, "error", raw["type"])
	assert.Equal(t, "INVALID_CHANNEL", raw["error_code"])
	assert.Equal(t, "channel pattern 'foo*bar' is invalid", raw["error_message"])
}

func TestServerMessage_Serialize_SystemMessage(t *testing.T) {
	msg := eventstream.ServerMessage{
		Type:          eventstream.ServerMessageTypeSystem,
		SystemMessage: "Connection established. Awaiting subscriptions.",
	}

	data, err := msg.Serialize()
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	assert.Equal(t, "system", raw["type"])
	assert.Equal(t, "Connection established. Awaiting subscriptions.", raw["system_message"])
}

func TestServerMessage_Serialize_JSONRoundTrip(t *testing.T) {
	ts := time.Now().UTC().Truncate(time.Second)
	payload := json.RawMessage(`{"key":"value"}`)

	original := eventstream.ServerMessage{
		Type:           eventstream.ServerMessageTypeEvent,
		SubscriptionID: "sub-roundtrip",
		Channel:        "some.channel",
		Event: &eventstream.EventPayload{
			EventID:       "evt-rt-1",
			EventType:     "some.event.v1",
			AggregateID:   "agg-rt",
			AggregateType: "SomeAggregate",
			TenantID:      "tenant-rt",
			CorrelationID: "corr-rt",
			CausationID:   "cause-rt",
			Timestamp:     ts,
			Payload:       payload,
		},
	}

	data, err := original.Serialize()
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))
	assert.Equal(t, "event", raw["type"])
	assert.Equal(t, "sub-roundtrip", raw["subscription_id"])
}

// --- Error codes ---

func TestErrorCodes_Values(t *testing.T) {
	assert.Equal(t, "INVALID_CHANNEL", string(eventstream.ErrorCodeInvalidChannel))
	assert.Equal(t, "UNAUTHORIZED_CHANNEL", string(eventstream.ErrorCodeUnauthorizedChannel))
	assert.Equal(t, "BUFFER_OVERFLOW", string(eventstream.ErrorCodeBufferOverflow))
	assert.Equal(t, "SUBSCRIPTION_LIMIT_EXCEEDED", string(eventstream.ErrorCodeSubscriptionLimitExceeded))
}

// --- EventPayload from DomainEvent ---

func TestNewEventPayload_FromDomainEvent(t *testing.T) {
	ts := time.Date(2026, 1, 15, 8, 30, 0, 0, time.UTC)
	event := eventstream.DomainEvent{
		EventID:       "evt-from-domain",
		EventType:     "payment_order.settled.v2",
		Channel:       "payment-order.settled",
		AggregateID:   "agg-domain",
		AggregateType: "PaymentOrder",
		TenantID:      "tenant-domain",
		CorrelationID: "corr-domain",
		CausationID:   "cause-domain",
		Timestamp:     ts,
		Payload:       []byte(`{"status":"settled"}`),
	}

	payload := eventstream.NewEventPayload(event)

	assert.Equal(t, "evt-from-domain", payload.EventID)
	assert.Equal(t, "payment_order.settled.v2", payload.EventType)
	assert.Equal(t, "agg-domain", payload.AggregateID)
	assert.Equal(t, "PaymentOrder", payload.AggregateType)
	assert.Equal(t, "tenant-domain", payload.TenantID)
	assert.Equal(t, "corr-domain", payload.CorrelationID)
	assert.Equal(t, "cause-domain", payload.CausationID)
	assert.Equal(t, ts, payload.Timestamp)
	assert.JSONEq(t, `{"status":"settled"}`, string(payload.Payload))
}

// --- ISO 8601 timestamp serialization ---

func TestEventPayload_TimestampSerializesAsISO8601(t *testing.T) {
	ts := time.Date(2026, 3, 14, 15, 9, 26, 535897932, time.UTC)
	payload := eventstream.EventPayload{
		EventID:   "ts-test",
		EventType: "test.event",
		Timestamp: ts,
		Payload:   json.RawMessage(`{}`),
	}

	data, err := json.Marshal(payload)
	require.NoError(t, err)

	var raw map[string]interface{}
	require.NoError(t, json.Unmarshal(data, &raw))

	tsStr, ok := raw["timestamp"].(string)
	require.True(t, ok)

	// Must parse as RFC3339 (ISO 8601)
	parsed, err := time.Parse(time.RFC3339Nano, tsStr)
	require.NoError(t, err, "timestamp must be valid ISO 8601 / RFC3339")
	assert.Equal(t, ts.Unix(), parsed.Unix())
}

// --- Client message type constants ---

func TestClientMessageType_Values(t *testing.T) {
	assert.Equal(t, "subscribe", string(eventstream.ClientMessageTypeSubscribe))
	assert.Equal(t, "unsubscribe", string(eventstream.ClientMessageTypeUnsubscribe))
}

// --- Server message type constants ---

func TestServerMessageType_Values(t *testing.T) {
	assert.Equal(t, "event", string(eventstream.ServerMessageTypeEvent))
	assert.Equal(t, "subscribed", string(eventstream.ServerMessageTypeSubscribed))
	assert.Equal(t, "error", string(eventstream.ServerMessageTypeError))
	assert.Equal(t, "system", string(eventstream.ServerMessageTypeSystem))
}

// --- OmitEmpty behavior ---

func TestServerMessage_Serialize_OmitsEmptyOptionalFields(t *testing.T) {
	// A "subscribed" message should not emit null/empty channel, event, error fields.
	msg := eventstream.ServerMessage{
		Type:           eventstream.ServerMessageTypeSubscribed,
		SubscriptionID: "sub-omit",
	}

	data, err := msg.Serialize()
	require.NoError(t, err)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &raw))

	_, hasChannel := raw["channel"]
	assert.False(t, hasChannel, "channel should be omitted when empty")

	_, hasEvent := raw["event"]
	assert.False(t, hasEvent, "event should be omitted when nil")

	_, hasErrorCode := raw["error_code"]
	assert.False(t, hasErrorCode, "error_code should be omitted when empty")

	_, hasErrorMsg := raw["error_message"]
	assert.False(t, hasErrorMsg, "error_message should be omitted when empty")

	_, hasSysMsg := raw["system_message"]
	assert.False(t, hasSysMsg, "system_message should be omitted when empty")
}
