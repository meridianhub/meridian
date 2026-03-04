package eventstream_test

import (
	"errors"
	"testing"

	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ChannelPattern.Matches ---

func TestChannelPattern_Matches(t *testing.T) {
	tests := []struct {
		name    string
		pattern eventstream.ChannelPattern
		channel string
		want    bool
	}{
		{
			name:    "exact match",
			pattern: "payment-order.created",
			channel: "payment-order.created",
			want:    true,
		},
		{
			name:    "exact match - no match on different channel",
			pattern: "payment-order.created",
			channel: "payment-order.updated",
			want:    false,
		},
		{
			name:    "prefix glob matches same prefix",
			pattern: "payment-order.*",
			channel: "payment-order.created",
			want:    true,
		},
		{
			name:    "prefix glob matches another suffix",
			pattern: "payment-order.*",
			channel: "payment-order.cancelled",
			want:    true,
		},
		{
			name:    "prefix glob does not match different prefix",
			pattern: "payment-order.*",
			channel: "position-keeping.created",
			want:    false,
		},
		{
			name:    "wildcard matches everything",
			pattern: "*",
			channel: "any.channel.at.all",
			want:    true,
		},
		{
			name:    "wildcard matches empty string",
			pattern: "*",
			channel: "",
			want:    true,
		},
		{
			name:    "prefix glob with no separator in channel",
			pattern: "payment-order.*",
			channel: "payment-order.",
			want:    true,
		},
		{
			name:    "exact - empty pattern does not match non-empty channel",
			pattern: "",
			channel: "some.channel",
			want:    false,
		},
		{
			name:    "exact - empty pattern matches empty channel",
			pattern: "",
			channel: "",
			want:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.pattern.Matches(tc.channel)
			assert.Equal(t, tc.want, got)
		})
	}
}

// --- NewDomainEvent ---

func TestNewDomainEvent_Success(t *testing.T) {
	payload := []byte(`{"foo":"bar"}`)
	event, err := eventstream.NewDomainEvent(
		"payment_order.created.v1",
		"payment-order.events.v1",
		"agg-123",
		"PaymentOrder",
		"tenant-abc",
		"corr-456",
		"cause-789",
		payload,
	)

	require.NoError(t, err)
	assert.NotEmpty(t, event.EventID)
	assert.Equal(t, "payment_order.created.v1", event.EventType)
	assert.Equal(t, "payment-order.events.v1", event.Topic)
	assert.Equal(t, "payment-order.events", event.Channel) // .v1 stripped
	assert.Equal(t, "agg-123", event.AggregateID)
	assert.Equal(t, "PaymentOrder", event.AggregateType)
	assert.Equal(t, "tenant-abc", event.TenantID)
	assert.Equal(t, "corr-456", event.CorrelationID)
	assert.Equal(t, "cause-789", event.CausationID)
	assert.Equal(t, payload, event.Payload)
	assert.False(t, event.Timestamp.IsZero())
}

func TestNewDomainEvent_UniqueEventIDs(t *testing.T) {
	e1, err1 := eventstream.NewDomainEvent("type.v1", "topic.v1", "", "", "tenant", "", "", nil)
	e2, err2 := eventstream.NewDomainEvent("type.v1", "topic.v1", "", "", "tenant", "", "", nil)

	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.NotEqual(t, e1.EventID, e2.EventID)
}

func TestNewDomainEvent_EmptyEventType_ReturnsError(t *testing.T) {
	_, err := eventstream.NewDomainEvent("", "topic.v1", "", "", "tenant", "", "", nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, eventstream.ErrEmptyEventType))
}

func TestNewDomainEvent_EmptyTopic_ReturnsError(t *testing.T) {
	_, err := eventstream.NewDomainEvent("type.v1", "", "", "", "tenant", "", "", nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, eventstream.ErrEmptyTopic))
}

func TestNewDomainEvent_EmptyTenantID_ReturnsError(t *testing.T) {
	_, err := eventstream.NewDomainEvent("type.v1", "topic.v1", "", "", "", "", "", nil)
	require.Error(t, err)
	assert.True(t, errors.Is(err, eventstream.ErrEmptyTenantID))
}

func TestNewDomainEvent_ChannelDerivation(t *testing.T) {
	tests := []struct {
		name            string
		topic           string
		expectedChannel string
	}{
		{
			name:            "strips .v1 suffix",
			topic:           "payment-order.events.v1",
			expectedChannel: "payment-order.events",
		},
		{
			name:            "strips .v2 suffix",
			topic:           "position-keeping.events.v2",
			expectedChannel: "position-keeping.events",
		},
		{
			name:            "strips .v10 suffix",
			topic:           "some.topic.v10",
			expectedChannel: "some.topic",
		},
		{
			name:            "no version suffix preserved as-is",
			topic:           "some.topic",
			expectedChannel: "some.topic",
		},
		{
			name:            "non-version suffix preserved",
			topic:           "some.topic.beta",
			expectedChannel: "some.topic.beta",
		},
		{
			name:            "single segment with no dot",
			topic:           "events",
			expectedChannel: "events",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			event, err := eventstream.NewDomainEvent("t.v1", tc.topic, "", "", "tenant", "", "", nil)
			require.NoError(t, err)
			assert.Equal(t, tc.expectedChannel, event.Channel)
		})
	}
}

// --- NewSubscription ---

func TestNewSubscription_Success(t *testing.T) {
	filters := eventstream.SubscriptionFilters{AggregateID: "agg-1"}
	sub, err := eventstream.NewSubscription(
		"sub-001",
		[]eventstream.ChannelPattern{"payment-order.*", "position-keeping.*"},
		filters,
	)

	require.NoError(t, err)
	assert.Equal(t, "sub-001", sub.ID)
	assert.Len(t, sub.Channels, 2)
	assert.Equal(t, filters, sub.Filters)
}

func TestNewSubscription_EmptyID_ReturnsError(t *testing.T) {
	_, err := eventstream.NewSubscription("", []eventstream.ChannelPattern{"channel.*"}, eventstream.SubscriptionFilters{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, eventstream.ErrEmptySubscriptionID))
}

func TestNewSubscription_NoChannels_ReturnsError(t *testing.T) {
	_, err := eventstream.NewSubscription("sub-001", nil, eventstream.SubscriptionFilters{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, eventstream.ErrNoChannels))
}

func TestNewSubscription_EmptyChannelPattern_ReturnsError(t *testing.T) {
	_, err := eventstream.NewSubscription(
		"sub-001",
		[]eventstream.ChannelPattern{"valid.*", ""},
		eventstream.SubscriptionFilters{},
	)
	require.Error(t, err)
	assert.True(t, errors.Is(err, eventstream.ErrEmptyChannelPattern))
}

func TestNewSubscription_InvalidWildcardPosition_ReturnsError(t *testing.T) {
	tests := []struct {
		name    string
		pattern eventstream.ChannelPattern
	}{
		{name: "wildcard in middle", pattern: "foo*bar"},
		{name: "wildcard at start only", pattern: "*bar"},
		{name: "double wildcard", pattern: "foo**"},
		{name: "wildcard not at end", pattern: "foo*.bar"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eventstream.NewSubscription(
				"sub-001",
				[]eventstream.ChannelPattern{tc.pattern},
				eventstream.SubscriptionFilters{},
			)
			require.Error(t, err)
			assert.True(t, errors.Is(err, eventstream.ErrInvalidChannelPattern))
		})
	}
}

func TestNewSubscription_ValidWildcardPositions_Succeed(t *testing.T) {
	tests := []struct {
		name    string
		pattern eventstream.ChannelPattern
	}{
		{name: "trailing wildcard", pattern: "payment.*"},
		{name: "standalone wildcard", pattern: "*"},
		{name: "exact no wildcard", pattern: "payment.created"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := eventstream.NewSubscription(
				"sub-001",
				[]eventstream.ChannelPattern{tc.pattern},
				eventstream.SubscriptionFilters{},
			)
			require.NoError(t, err)
		})
	}
}

// --- Subscription.Matches ---

func TestSubscription_Matches(t *testing.T) {
	makeEvent := func(channel, aggregateID, correlationID string) eventstream.DomainEvent {
		return eventstream.DomainEvent{
			Channel:       channel,
			AggregateID:   aggregateID,
			CorrelationID: correlationID,
		}
	}

	tests := []struct {
		name    string
		sub     eventstream.Subscription
		event   eventstream.DomainEvent
		matches bool
	}{
		{
			name: "channel match, no filters",
			sub: eventstream.Subscription{
				ID:       "s1",
				Channels: []eventstream.ChannelPattern{"payment-order.*"},
			},
			event:   makeEvent("payment-order.created", "", ""),
			matches: true,
		},
		{
			name: "channel mismatch",
			sub: eventstream.Subscription{
				ID:       "s2",
				Channels: []eventstream.ChannelPattern{"payment-order.*"},
			},
			event:   makeEvent("position-keeping.created", "", ""),
			matches: false,
		},
		{
			name: "channel match, aggregate filter matches",
			sub: eventstream.Subscription{
				ID:       "s3",
				Channels: []eventstream.ChannelPattern{"*"},
				Filters:  eventstream.SubscriptionFilters{AggregateID: "agg-1"},
			},
			event:   makeEvent("any.channel", "agg-1", ""),
			matches: true,
		},
		{
			name: "channel match, aggregate filter does not match",
			sub: eventstream.Subscription{
				ID:       "s4",
				Channels: []eventstream.ChannelPattern{"*"},
				Filters:  eventstream.SubscriptionFilters{AggregateID: "agg-1"},
			},
			event:   makeEvent("any.channel", "agg-2", ""),
			matches: false,
		},
		{
			name: "channel match, correlation filter matches",
			sub: eventstream.Subscription{
				ID:       "s5",
				Channels: []eventstream.ChannelPattern{"*"},
				Filters:  eventstream.SubscriptionFilters{CorrelationID: "corr-abc"},
			},
			event:   makeEvent("any.channel", "", "corr-abc"),
			matches: true,
		},
		{
			name: "channel match, correlation filter does not match",
			sub: eventstream.Subscription{
				ID:       "s6",
				Channels: []eventstream.ChannelPattern{"*"},
				Filters:  eventstream.SubscriptionFilters{CorrelationID: "corr-abc"},
			},
			event:   makeEvent("any.channel", "", "corr-xyz"),
			matches: false,
		},
		{
			name: "channel match, both filters match",
			sub: eventstream.Subscription{
				ID:       "s7",
				Channels: []eventstream.ChannelPattern{"payment-order.*"},
				Filters: eventstream.SubscriptionFilters{
					AggregateID:   "agg-1",
					CorrelationID: "corr-abc",
				},
			},
			event:   makeEvent("payment-order.created", "agg-1", "corr-abc"),
			matches: true,
		},
		{
			name: "channel match, one filter matches but other does not",
			sub: eventstream.Subscription{
				ID:       "s8",
				Channels: []eventstream.ChannelPattern{"*"},
				Filters: eventstream.SubscriptionFilters{
					AggregateID:   "agg-1",
					CorrelationID: "corr-abc",
				},
			},
			event:   makeEvent("any.channel", "agg-1", "corr-xyz"),
			matches: false,
		},
		{
			name: "multiple channels, second matches",
			sub: eventstream.Subscription{
				ID:       "s9",
				Channels: []eventstream.ChannelPattern{"payment-order.*", "position-keeping.*"},
			},
			event:   makeEvent("position-keeping.updated", "", ""),
			matches: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.sub.Matches(tc.event)
			assert.Equal(t, tc.matches, got)
		})
	}
}
