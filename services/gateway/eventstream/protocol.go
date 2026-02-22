package eventstream

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// ErrEmptyClientMessage is returned by ParseClientMessage when the input is nil or empty.
var ErrEmptyClientMessage = errors.New("eventstream: cannot parse empty client message")

// ClientMessageType identifies the type of a message sent from the client to the server.
type ClientMessageType string

const (
	// ClientMessageTypeSubscribe requests a new subscription on one or more channels.
	ClientMessageTypeSubscribe ClientMessageType = "subscribe"

	// ClientMessageTypeUnsubscribe cancels an existing subscription identified by ID.
	ClientMessageTypeUnsubscribe ClientMessageType = "unsubscribe"
)

// ServerMessageType identifies the type of a message sent from the server to the client.
type ServerMessageType string

const (
	// ServerMessageTypeEvent delivers a domain event to the client.
	ServerMessageTypeEvent ServerMessageType = "event"

	// ServerMessageTypeSubscribed confirms that a subscription was created.
	ServerMessageTypeSubscribed ServerMessageType = "subscribed"

	// ServerMessageTypeError reports an error related to a client request.
	ServerMessageTypeError ServerMessageType = "error"

	// ServerMessageTypeSystem carries informational messages from the server (e.g. connection state).
	ServerMessageTypeSystem ServerMessageType = "system"
)

// ErrorCode is a machine-readable code sent in error messages.
type ErrorCode string

const (
	// ErrorCodeInvalidChannel is returned when a channel pattern is syntactically invalid.
	ErrorCodeInvalidChannel ErrorCode = "INVALID_CHANNEL"

	// ErrorCodeUnauthorizedChannel is returned when the client is not permitted to subscribe
	// to the requested channel.
	ErrorCodeUnauthorizedChannel ErrorCode = "UNAUTHORIZED_CHANNEL"

	// ErrorCodeBufferOverflow is returned when the server's delivery buffer for the client
	// is full and events cannot be queued.
	ErrorCodeBufferOverflow ErrorCode = "BUFFER_OVERFLOW"

	// ErrorCodeSubscriptionLimitExceeded is returned when the client has reached the
	// maximum number of concurrent subscriptions.
	ErrorCodeSubscriptionLimitExceeded ErrorCode = "SUBSCRIPTION_LIMIT_EXCEEDED"
)

// ClientMessage is a message sent from the WebSocket client to the gateway.
type ClientMessage struct {
	// Type identifies the action the client is requesting.
	Type ClientMessageType `json:"type"`

	// ID is an opaque identifier chosen by the client to correlate responses.
	ID string `json:"id"`

	// Channels lists the channel patterns the client wishes to subscribe to.
	// Only applicable for subscribe messages.
	Channels []ChannelPattern `json:"channels,omitempty"`

	// Filters optionally narrows which events are delivered within the subscribed channels.
	// Only applicable for subscribe messages.
	Filters SubscriptionFilters `json:"filters,omitempty"`
}

// ParseClientMessage deserialises a raw WebSocket frame into a ClientMessage.
// Returns an error if data is nil, empty, or not valid JSON.
func ParseClientMessage(data []byte) (ClientMessage, error) {
	if len(data) == 0 {
		return ClientMessage{}, ErrEmptyClientMessage
	}
	var msg ClientMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return ClientMessage{}, fmt.Errorf("eventstream: malformed client message: %w", err)
	}
	return msg, nil
}

// EventPayload is the event body carried inside a ServerMessage of type "event".
// Timestamp is serialized as an ISO 8601 / RFC 3339 string.
type EventPayload struct {
	// EventID is the globally unique identifier for this event instance.
	EventID string `json:"event_id"`

	// EventType identifies the event schema (e.g. "payment_order.created.v1").
	EventType string `json:"event_type"`

	// AggregateID is the ID of the aggregate that produced this event.
	AggregateID string `json:"aggregate_id,omitempty"`

	// AggregateType is the type of aggregate (e.g. "PaymentOrder").
	AggregateType string `json:"aggregate_type,omitempty"`

	// TenantID identifies the tenant that owns this event.
	// Always present: domain.NewDomainEvent enforces a non-empty tenant ID.
	TenantID string `json:"tenant_id"`

	// CorrelationID links related events across services.
	CorrelationID string `json:"correlation_id,omitempty"`

	// CausationID identifies the event or command that caused this event.
	CausationID string `json:"causation_id,omitempty"`

	// Timestamp is when the event occurred, serialized as ISO 8601 UTC.
	Timestamp time.Time `json:"timestamp"`

	// Payload is the JSON-encoded event body, embedded verbatim in the wire message.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewEventPayload constructs an EventPayload from a DomainEvent, mapping
// all fields across without transformation.
func NewEventPayload(event DomainEvent) EventPayload {
	return EventPayload{
		EventID:       event.EventID,
		EventType:     event.EventType,
		AggregateID:   event.AggregateID,
		AggregateType: event.AggregateType,
		TenantID:      event.TenantID,
		CorrelationID: event.CorrelationID,
		CausationID:   event.CausationID,
		Timestamp:     event.Timestamp,
		Payload:       json.RawMessage(event.Payload),
	}
}

// ServerMessage is a message sent from the gateway to the WebSocket client.
// Fields not relevant to the message type are omitted from the JSON output.
type ServerMessage struct {
	// Type identifies the nature of this server message.
	Type ServerMessageType `json:"type"`

	// SubscriptionID is the ID of the subscription this message relates to.
	// Present on "event" and "subscribed" messages.
	SubscriptionID string `json:"subscription_id,omitempty"`

	// Channel is the logical event channel on which the event was received.
	// Present on "event" messages.
	Channel string `json:"channel,omitempty"`

	// Event carries the domain event payload. Present on "event" messages.
	Event *EventPayload `json:"event,omitempty"`

	// ErrorCode is a machine-readable error identifier. Present on "error" messages.
	ErrorCode ErrorCode `json:"error_code,omitempty"`

	// ErrorMessage is a human-readable description of the error. Present on "error" messages.
	ErrorMessage string `json:"error_message,omitempty"`

	// SystemMessage is an informational message from the server. Present on "system" messages.
	SystemMessage string `json:"system_message,omitempty"`
}

// Serialize encodes the ServerMessage to JSON for transmission over the WebSocket connection.
// Returns an error if JSON marshaling fails.
func (m ServerMessage) Serialize() ([]byte, error) {
	data, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("eventstream: failed to serialize server message: %w", err)
	}
	return data, nil
}
