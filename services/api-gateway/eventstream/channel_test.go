package eventstream_test

import (
	"errors"
	"testing"

	"github.com/meridianhub/meridian/services/api-gateway/eventstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- DeriveChannel ---

func TestDeriveChannel(t *testing.T) {
	tests := []struct {
		name            string
		topic           string
		expectedChannel string
		wantErr         error
	}{
		// Version suffix stripping
		{
			name:            "strips .v1 suffix",
			topic:           "payment-order.reserved.v1",
			expectedChannel: "payment-order.reserved",
		},
		{
			name:            "strips .v2 suffix",
			topic:           "position-keeping.transaction-updated.v2",
			expectedChannel: "position-keeping.transaction-updated",
		},
		{
			name:            "strips .v10 suffix",
			topic:           "current-account.events.v10",
			expectedChannel: "current-account.events",
		},
		{
			name:            "no version suffix preserved as-is",
			topic:           "payment-order.reserved",
			expectedChannel: "payment-order.reserved",
		},
		// Audit topics
		{
			name:            "audit topic strips version suffix",
			topic:           "audit.events.current-account.v1",
			expectedChannel: "audit.events.current-account",
		},
		{
			name:            "audit topic without version",
			topic:           "audit.events.payment-order",
			expectedChannel: "audit.events.payment-order",
		},
		// Single segment (no dots)
		{
			name:            "single segment no dot",
			topic:           "events",
			expectedChannel: "events",
		},
		// Non-version suffix preserved
		{
			name:            "non-version suffix preserved",
			topic:           "payment-order.beta",
			expectedChannel: "payment-order.beta",
		},
		// Error cases
		{
			name:    "empty topic returns error",
			topic:   "",
			wantErr: eventstream.ErrEmptyTopic,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := eventstream.DeriveChannel(tc.topic)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tc.wantErr))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.expectedChannel, got)
		})
	}
}

// --- ValidateChannelPattern ---

func TestValidateChannelPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr error
	}{
		{
			name:    "exact match pattern is valid",
			pattern: "payment-order.reserved",
		},
		{
			name:    "service wildcard is valid",
			pattern: "payment-order.*",
		},
		{
			name:    "prefix wildcard with hyphen is valid",
			pattern: "position-keeping.transaction-*",
		},
		{
			name:    "firehose wildcard is valid",
			pattern: "*",
		},
		{
			name:    "empty pattern returns error",
			pattern: "",
			wantErr: eventstream.ErrEmptyChannelPattern,
		},
		{
			name:    "wildcard in middle returns error",
			pattern: "foo*bar",
			wantErr: eventstream.ErrInvalidChannelPattern,
		},
		{
			name:    "wildcard at start not end returns error",
			pattern: "*bar",
			wantErr: eventstream.ErrInvalidChannelPattern,
		},
		{
			name:    "wildcard not at end returns error",
			pattern: "foo*.bar",
			wantErr: eventstream.ErrInvalidChannelPattern,
		},
		{
			name:    "double wildcard returns error",
			pattern: "foo**",
			wantErr: eventstream.ErrInvalidChannelPattern,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := eventstream.ValidateChannelPattern(tc.pattern)
			if tc.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tc.wantErr))
				return
			}
			require.NoError(t, err)
		})
	}
}

// --- ChannelMatcher ---

func TestChannelMatcher_Matches(t *testing.T) {
	tests := []struct {
		name     string
		patterns []eventstream.ChannelPattern
		channel  string
		want     bool
	}{
		{
			name:     "exact match",
			patterns: []eventstream.ChannelPattern{"payment-order.reserved"},
			channel:  "payment-order.reserved",
			want:     true,
		},
		{
			name:     "exact match - no match on different channel",
			patterns: []eventstream.ChannelPattern{"payment-order.reserved"},
			channel:  "payment-order.initiated",
			want:     false,
		},
		{
			name:     "service wildcard matches",
			patterns: []eventstream.ChannelPattern{"payment-order.*"},
			channel:  "payment-order.initiated",
			want:     true,
		},
		{
			name:     "service wildcard does not match different service",
			patterns: []eventstream.ChannelPattern{"payment-order.*"},
			channel:  "position-keeping.created",
			want:     false,
		},
		{
			name:     "prefix wildcard with hyphen matches",
			patterns: []eventstream.ChannelPattern{"position-keeping.transaction-*"},
			channel:  "position-keeping.transaction-updated",
			want:     true,
		},
		{
			name:     "prefix wildcard with hyphen does not match different prefix",
			patterns: []eventstream.ChannelPattern{"position-keeping.transaction-*"},
			channel:  "position-keeping.account-updated",
			want:     false,
		},
		{
			name:     "firehose matches everything",
			patterns: []eventstream.ChannelPattern{"*"},
			channel:  "any.channel.at.all",
			want:     true,
		},
		{
			name:     "firehose matches empty channel",
			patterns: []eventstream.ChannelPattern{"*"},
			channel:  "",
			want:     true,
		},
		{
			name:     "multiple patterns - first matches",
			patterns: []eventstream.ChannelPattern{"payment-order.*", "position-keeping.*"},
			channel:  "payment-order.reserved",
			want:     true,
		},
		{
			name:     "multiple patterns - second matches",
			patterns: []eventstream.ChannelPattern{"payment-order.*", "position-keeping.*"},
			channel:  "position-keeping.updated",
			want:     true,
		},
		{
			name:     "multiple patterns - none match",
			patterns: []eventstream.ChannelPattern{"payment-order.*", "position-keeping.*"},
			channel:  "current-account.created",
			want:     false,
		},
		{
			name:     "empty patterns - never matches",
			patterns: []eventstream.ChannelPattern{},
			channel:  "payment-order.reserved",
			want:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			matcher := eventstream.NewChannelMatcher(tc.patterns...)
			got := matcher.Matches(tc.channel)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestChannelMatcher_Add(t *testing.T) {
	matcher := eventstream.NewChannelMatcher("payment-order.*")

	// Does not match before adding
	assert.False(t, matcher.Matches("position-keeping.created"))

	// Add a new pattern
	matcher.Add("position-keeping.*")

	// Now matches
	assert.True(t, matcher.Matches("position-keeping.created"))
}

func TestChannelMatcher_MatchesSubscription(t *testing.T) {
	sub, err := eventstream.NewSubscription(
		"sub-1",
		[]eventstream.ChannelPattern{"payment-order.*", "position-keeping.*"},
		eventstream.SubscriptionFilters{},
	)
	require.NoError(t, err)

	matcher := eventstream.NewChannelMatcherFromSubscription(sub)

	assert.True(t, matcher.Matches("payment-order.reserved"))
	assert.True(t, matcher.Matches("position-keeping.updated"))
	assert.False(t, matcher.Matches("current-account.created"))
}
