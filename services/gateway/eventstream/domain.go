package eventstream

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Sentinel errors returned by domain constructors and validation.
var (
	// ErrEmptyEventType is returned when an empty event type is provided.
	ErrEmptyEventType = errors.New("event type cannot be empty")

	// ErrEmptyTopic is returned when an empty topic is provided.
	ErrEmptyTopic = errors.New("topic cannot be empty")

	// ErrEmptyTenantID is returned when an empty tenant ID is provided.
	ErrEmptyTenantID = errors.New("tenant ID cannot be empty")

	// ErrEmptySubscriptionID is returned when an empty subscription ID is provided.
	ErrEmptySubscriptionID = errors.New("subscription ID cannot be empty")

	// ErrNoChannels is returned when a subscription has no channel patterns.
	ErrNoChannels = errors.New("subscription must have at least one channel pattern")

	// ErrEmptyChannelPattern is returned when an empty channel pattern is provided.
	ErrEmptyChannelPattern = errors.New("channel pattern cannot be empty")
)

// ChannelPattern is a string that identifies a logical event channel.
// Glob-style prefix matching is supported via a trailing "*" wildcard.
//
// Examples:
//
//	"payment-order.created"    // exact match
//	"payment-order.*"          // all payment-order events
//	"*"                        // all channels
type ChannelPattern string

// Matches reports whether the given channel name satisfies this pattern.
// The only wildcard supported is a trailing "*" which matches any suffix.
func (p ChannelPattern) Matches(channel string) bool {
	pattern := string(p)
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(channel, prefix)
	}
	return pattern == channel
}

// DomainEvent is the canonical event envelope that flows through the gateway
// streaming pipeline. Payload is JSON-encoded so gateway adapters remain format-agnostic.
type DomainEvent struct {
	// EventID is a globally unique identifier for this event instance.
	EventID string

	// EventType identifies the event schema (e.g., "payment_order.created.v1").
	EventType string

	// Topic is the Kafka topic or equivalent source topic for this event.
	Topic string

	// Channel is the logical routing channel derived from Topic by stripping
	// trailing version suffixes (e.g., ".v1"). Used for subscription matching.
	Channel string

	// AggregateID is the ID of the aggregate that produced this event.
	AggregateID string

	// AggregateType is the type of aggregate (e.g., "PaymentOrder").
	AggregateType string

	// TenantID identifies the tenant that owns this event.
	TenantID string

	// CorrelationID links related events across services.
	CorrelationID string

	// CausationID identifies the event or command that caused this event.
	CausationID string

	// Timestamp is when the event occurred.
	Timestamp time.Time

	// Payload is the JSON-encoded event body. Gateway adapters decode this
	// from the wire format (e.g., protobuf→JSON) before populating this field.
	Payload []byte
}

// NewDomainEvent constructs a DomainEvent with a generated EventID and the Channel
// derived from Topic by stripping trailing ".vN" version suffixes.
//
// Returns ErrEmptyEventType if eventType is empty.
// Returns ErrEmptyTopic if topic is empty.
// Returns ErrEmptyTenantID if tenantID is empty.
func NewDomainEvent(
	eventType string,
	topic string,
	aggregateID string,
	aggregateType string,
	tenantID string,
	correlationID string,
	causationID string,
	payload []byte,
) (DomainEvent, error) {
	if eventType == "" {
		return DomainEvent{}, ErrEmptyEventType
	}
	if topic == "" {
		return DomainEvent{}, ErrEmptyTopic
	}
	if tenantID == "" {
		return DomainEvent{}, ErrEmptyTenantID
	}

	return DomainEvent{
		EventID:       uuid.New().String(),
		EventType:     eventType,
		Topic:         topic,
		Channel:       deriveChannel(topic),
		AggregateID:   aggregateID,
		AggregateType: aggregateType,
		TenantID:      tenantID,
		CorrelationID: correlationID,
		CausationID:   causationID,
		Timestamp:     time.Now().UTC(),
		Payload:       payload,
	}, nil
}

// deriveChannel converts a topic string to a channel name by stripping trailing
// version suffixes of the form ".vN" (e.g., ".v1", ".v2").
func deriveChannel(topic string) string {
	// Strip trailing ".vN" suffix where N is one or more digits.
	// Walk backwards to find the suffix and strip it.
	dot := strings.LastIndex(topic, ".")
	if dot < 0 {
		return topic
	}
	suffix := topic[dot+1:]
	if len(suffix) > 1 && suffix[0] == 'v' {
		allDigits := true
		for _, c := range suffix[1:] {
			if c < '0' || c > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return topic[:dot]
		}
	}
	return topic
}

// SubscriptionFilters contains optional filters that narrow which events a
// subscription receives. An empty filter matches all events on the subscribed channels.
type SubscriptionFilters struct {
	// AggregateID, when non-empty, restricts delivery to events from this aggregate.
	AggregateID string

	// CorrelationID, when non-empty, restricts delivery to events with this correlation ID.
	CorrelationID string
}

// Subscription describes a client's interest in one or more event channels.
type Subscription struct {
	// ID is a unique identifier for this subscription.
	ID string

	// Channels lists the channel patterns this subscription matches.
	Channels []ChannelPattern

	// Filters narrows event delivery within the matched channels.
	Filters SubscriptionFilters
}

// NewSubscription constructs a Subscription with the provided ID and channel patterns.
//
// Returns ErrEmptySubscriptionID if id is empty.
// Returns ErrNoChannels if channels is empty.
// Returns ErrEmptyChannelPattern if any channel pattern is empty.
func NewSubscription(id string, channels []ChannelPattern, filters SubscriptionFilters) (Subscription, error) {
	if id == "" {
		return Subscription{}, ErrEmptySubscriptionID
	}
	if len(channels) == 0 {
		return Subscription{}, ErrNoChannels
	}
	for _, ch := range channels {
		if ch == "" {
			return Subscription{}, ErrEmptyChannelPattern
		}
	}
	return Subscription{
		ID:       id,
		Channels: channels,
		Filters:  filters,
	}, nil
}

// Matches reports whether this subscription should receive the given event.
// An event matches if at least one channel pattern matches the event's Channel,
// and all non-empty filter fields match the event's corresponding fields.
func (s *Subscription) Matches(event DomainEvent) bool {
	channelMatch := false
	for _, pattern := range s.Channels {
		if pattern.Matches(event.Channel) {
			channelMatch = true
			break
		}
	}
	if !channelMatch {
		return false
	}

	if s.Filters.AggregateID != "" && s.Filters.AggregateID != event.AggregateID {
		return false
	}
	if s.Filters.CorrelationID != "" && s.Filters.CorrelationID != event.CorrelationID {
		return false
	}
	return true
}
